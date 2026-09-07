package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
)

const (
	maxCodexUsageBodyBytes = 1 << 20
	codexUsageTimeout      = 20 * time.Second
)

func (s *Server) refreshCodexQuota(ctx context.Context, account model.Account) (model.Account, error) {
	if account.Provider != model.ProviderCodex || account.AuthType != "oauth" {
		return account, errors.New("account is not a Codex OAuth account")
	}
	if strings.TrimSpace(s.cfg.Providers.CodexUsage) == "" {
		return account, errors.New("Codex usage endpoint is not configured")
	}

	checkedAt := time.Now().UTC()
	requestContext, cancel := context.WithTimeout(ctx, codexUsageTimeout)
	defer cancel()
	credentials, err := s.repo.Credentials(requestContext, account)
	if err == nil {
		credentials, err = s.credentialsForRequest(requestContext, account, credentials, false)
	}
	if err != nil {
		return account, s.storeQuotaError(ctx, account, err)
	}

	body, status, err := s.fetchCodexUsage(requestContext, account, credentials)
	if err == nil && status == http.StatusUnauthorized && credentials.RefreshToken != "" {
		credentials, err = s.credentialsForRequest(requestContext, account, credentials, true)
		if err == nil {
			body, status, err = s.fetchCodexUsage(requestContext, account, credentials)
		}
	}
	if err != nil {
		return account, s.storeQuotaError(ctx, account, err)
	}
	if status < 200 || status >= 300 {
		return account, s.storeQuotaError(ctx, account, fmt.Errorf("Codex usage endpoint returned HTTP %d", status))
	}
	quota, err := normalizeCodexUsage(account.Quota, body)
	if err != nil {
		return account, s.storeQuotaError(ctx, account, err)
	}
	if err := s.repo.UpdateAccountQuota(ctx, account.ID, quota, checkedAt, nil); err != nil {
		return account, err
	}
	return s.repo.GetAccount(ctx, account.ID)
}

func (s *Server) fetchCodexUsage(ctx context.Context, account model.Account, credentials model.Credentials) ([]byte, int, error) {
	client, err := s.clientForAccount(account)
	if err != nil {
		return nil, 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.Providers.CodexUsage, nil)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Authorization", "Bearer "+credentials.Bearer())
	request.Header.Set("Accept", "application/json")
	applyCodexIdentityHeaders(request.Header)
	if accountID := strings.TrimSpace(credentials.ChatGPTAccountID); accountID != "" {
		request.Header.Set("ChatGPT-Account-Id", accountID)
	}

	response, err := doHTTP(client, request)
	if err != nil {
		return nil, 0, errors.New("could not contact the Codex usage endpoint")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxCodexUsageBodyBytes+1))
	if err != nil {
		return nil, response.StatusCode, errors.New("could not read the Codex usage response")
	}
	if len(body) > maxCodexUsageBodyBytes {
		return nil, response.StatusCode, errors.New("Codex usage response is too large")
	}
	return body, response.StatusCode, nil
}

func normalizeCodexUsage(existing json.RawMessage, body []byte) (json.RawMessage, error) {
	var payload struct {
		PlanType  string `json:"plan_type"`
		RateLimit *struct {
			LimitReached    bool            `json:"limit_reached"`
			PrimaryWindow   *codexRateWindow `json:"primary_window"`
			SecondaryWindow *codexRateWindow `json:"secondary_window"`
		} `json:"rate_limit"`
		Credits *struct {
			Balance any `json:"balance"`
		} `json:"credits"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, errors.New("Codex usage response is not valid JSON")
	}

	quota := map[string]any{}
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &quota)
	}
	if tier := strings.TrimSpace(payload.PlanType); tier != "" {
		quota["subscription_tier"] = tier
	}

	windows := make([]any, 0, 2)
	var primaryReset int64
	if payload.RateLimit != nil && payload.RateLimit.PrimaryWindow != nil {
		primary := payload.RateLimit.PrimaryWindow
		primaryReset = primary.ResetAt
		if window, ok := normalizeCodexWindow(primary, "", 0); ok {
			windows = append(windows, window)
		}
	}
	if payload.RateLimit != nil && payload.RateLimit.SecondaryWindow != nil {
		if window, ok := normalizeCodexWindow(payload.RateLimit.SecondaryWindow, "secondary", primaryReset); ok {
			windows = append(windows, window)
		}
	}
	if len(windows) == 0 && payload.Credits == nil {
		return nil, errors.New("Codex usage response did not contain recognizable usage fields")
	}
	if len(windows) > 0 {
		quota["windows"] = windows
	}

	if payload.Credits != nil {
		if balance, ok := asFloat64(payload.Credits.Balance); ok {
			quota["credit_balance"] = map[string]any{"balance": balance}
		}
	}

	encoded, err := json.Marshal(quota)
	if err != nil {
		return nil, errors.New("could not encode Codex usage")
	}
	return encoded, nil
}

type codexRateWindow struct {
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
	UsedPercent        float64 `json:"used_percent"`
	ResetAt            int64   `json:"reset_at"`
	ResetAfterSeconds  int64   `json:"reset_after_seconds"`
}

func normalizeCodexWindow(window *codexRateWindow, role string, primaryReset int64) (map[string]any, bool) {
	if window == nil {
		return nil, false
	}
	name := codexWindowName(window.LimitWindowSeconds, role, window.ResetAt, primaryReset)
	used := clampPercent(window.UsedPercent)
	result := map[string]any{
		"name":              name,
		"used_percent":      used,
		"remaining_percent": 100 - used,
	}
	if window.ResetAt > 0 {
		result["reset_at"] = time.Unix(window.ResetAt, 0).UTC().Format(time.RFC3339Nano)
	} else if window.ResetAfterSeconds > 0 {
		result["reset_at"] = time.Now().UTC().Add(time.Duration(window.ResetAfterSeconds) * time.Second).Format(time.RFC3339Nano)
	}
	return result, true
}

func codexWindowName(limitSeconds int64, role string, resetAt, primaryReset int64) string {
	hours := float64(limitSeconds) / 3600
	switch {
	case limitSeconds <= 0 && role == "secondary":
		return "seven_day"
	case limitSeconds <= 0:
		return "five_hour"
	case hours >= 140:
		return "seven_day"
	case hours >= 20 && hours <= 30:
		if resetAt > 0 && primaryReset > 0 && resetAt-primaryReset >= 3*24*3600 {
			return "seven_day"
		}
		return "daily"
	case hours >= 4 && hours <= 6:
		return "five_hour"
	default:
		if role == "secondary" {
			return "seven_day"
		}
		return "five_hour"
	}
}
