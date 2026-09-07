package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
)

const (
	maxClaudeUsageBodyBytes = 1 << 20
	claudeUsageTimeout      = 20 * time.Second
)

func (s *Server) refreshClaudeQuota(ctx context.Context, account model.Subscription) (model.Subscription, error) {
	if account.Provider != model.ProviderClaude || account.AuthType != "oauth" {
		return account, errors.New("account is not a Claude OAuth account")
	}
	if strings.TrimSpace(s.cfg.Providers.ClaudeUsage) == "" {
		return account, errors.New("Claude usage endpoint is not configured")
	}

	checkedAt := time.Now().UTC()
	requestContext, cancel := context.WithTimeout(ctx, claudeUsageTimeout)
	defer cancel()
	credentials, err := s.repo.Credentials(requestContext, account)
	if err == nil {
		credentials, err = s.credentialsForRequest(requestContext, account, credentials, false)
	}
	if err != nil {
		return account, s.storeQuotaError(ctx, account, err)
	}

	body, status, err := s.fetchClaudeUsage(requestContext, account, credentials)
	if err == nil && status == http.StatusUnauthorized && credentials.RefreshToken != "" {
		credentials, err = s.credentialsForRequest(requestContext, account, credentials, true)
		if err == nil {
			body, status, err = s.fetchClaudeUsage(requestContext, account, credentials)
		}
	}
	if err != nil {
		return account, s.storeQuotaError(ctx, account, err)
	}
	if status < 200 || status >= 300 {
		return account, s.storeQuotaError(ctx, account, fmt.Errorf("Claude usage endpoint returned HTTP %d", status))
	}
	quota, err := normalizeClaudeUsage(account.Quota, body)
	if err != nil {
		return account, s.storeQuotaError(ctx, account, err)
	}
	if err := s.repo.UpdateSubscriptionQuota(ctx, account.ID, quota, checkedAt, nil); err != nil {
		return account, err
	}
	return s.repo.GetSubscription(ctx, account.ID)
}

func (s *Server) fetchClaudeUsage(ctx context.Context, account model.Subscription, credentials model.Credentials) ([]byte, int, error) {
	client, err := s.clientForSubscription(account)
	if err != nil {
		return nil, 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.Providers.ClaudeUsage, nil)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Authorization", "Bearer "+credentials.Bearer())
	request.Header.Set("Accept", "application/json")
	request.Header.Set("anthropic-version", "2023-06-01")
	request.Header.Set("anthropic-beta", claudeBetaOAuth)
	request.Header.Set("User-Agent", defaultClaudeCLIUserAgent)
	request.Header.Set("x-app", "cli")

	response, err := doHTTP(client, request)
	if err != nil {
		return nil, 0, errors.New("could not contact the Claude usage endpoint")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxClaudeUsageBodyBytes+1))
	if err != nil {
		return nil, response.StatusCode, errors.New("could not read the Claude usage response")
	}
	if len(body) > maxClaudeUsageBodyBytes {
		return nil, response.StatusCode, errors.New("Claude usage response is too large")
	}
	return body, response.StatusCode, nil
}

func normalizeClaudeUsage(existing json.RawMessage, body []byte) (json.RawMessage, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, errors.New("Claude usage response is not valid JSON")
	}

	quota := map[string]any{}
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &quota)
	}

	windows := make([]any, 0, 6)
	for _, name := range []string{"five_hour", "seven_day", "seven_day_sonnet", "seven_day_opus", "seven_day_oauth_apps", "seven_day_cowork"} {
		window, ok := payload[name].(map[string]any)
		if !ok || window == nil {
			continue
		}
		normalized, ok := normalizeClaudeWindow(name, window)
		if ok {
			windows = append(windows, normalized)
		}
	}
	if limits, ok := payload["limits"].([]any); ok {
		for _, item := range limits {
			limit, ok := item.(map[string]any)
			if !ok {
				continue
			}
			name := strings.TrimSpace(fmt.Sprint(limit["kind"]))
			if name == "" {
				continue
			}
			if _, exists := payload[name]; exists {
				continue
			}
			if normalized, ok := normalizeClaudeWindow(name, limit); ok {
				windows = append(windows, normalized)
			}
		}
	}
	if len(windows) == 0 {
		return nil, errors.New("Claude usage response did not contain recognizable usage windows")
	}
	quota["windows"] = windows

	if extra, ok := payload["extra_usage"].(map[string]any); ok {
		creditBalance := map[string]any{}
		if enabled, _ := extra["is_enabled"].(bool); enabled {
			creditBalance["extra_usage_enabled"] = true
		}
		if used, ok := asFloat64(extra["used_credits"]); ok {
			creditBalance["used_credits"] = used
		}
		if limit, ok := asFloat64(extra["monthly_limit"]); ok {
			creditBalance["monthly_limit"] = limit
			if used, hasUsed := asFloat64(extra["used_credits"]); hasUsed {
				creditBalance["remaining"] = max(0, limit-used)
			}
		}
		if utilization, ok := asFloat64(extra["utilization"]); ok {
			creditBalance["used_percent"] = clampPercent(utilization)
		}
		if len(creditBalance) > 0 {
			quota["credit_balance"] = creditBalance
		}
	}

	encoded, err := json.Marshal(quota)
	if err != nil {
		return nil, errors.New("could not encode Claude usage")
	}
	return encoded, nil
}

func normalizeClaudeWindow(name string, window map[string]any) (map[string]any, bool) {
	utilization, hasUtilization := asFloat64(window["utilization"])
	if !hasUtilization {
		utilization, hasUtilization = asFloat64(window["percent"])
	}
	resetAt := ""
	if raw, ok := window["resets_at"].(string); ok {
		if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			resetAt = parsed.UTC().Format(time.RFC3339Nano)
		} else if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			resetAt = parsed.UTC().Format(time.RFC3339Nano)
		} else {
			resetAt = strings.TrimSpace(raw)
		}
	}
	if !hasUtilization && resetAt == "" {
		return nil, false
	}
	result := map[string]any{"name": name}
	if hasUtilization {
		used := clampPercent(utilization)
		result["used_percent"] = used
		result["remaining_percent"] = 100 - used
	}
	if resetAt != "" {
		result["reset_at"] = resetAt
	}
	return result, true
}

func asFloat64(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(typed, "%")), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
