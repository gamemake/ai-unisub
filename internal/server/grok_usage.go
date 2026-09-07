package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/ai-unisub/ai-unisub/internal/model"
	"github.com/gin-gonic/gin"
)

const (
	maxGrokBillingBodyBytes = 1 << 20
	grokBillingTimeout      = 20 * time.Second
)

func (s *Server) refreshSubscriptionUsage(c *gin.Context) {
	account, ok := s.subscriptionByParam(c)
	if !ok {
		return
	}
	if account.AuthType != "oauth" || (account.Provider != model.ProviderGrok && account.Provider != model.ProviderClaude && account.Provider != model.ProviderCodex) {
		apiError(c, http.StatusUnprocessableEntity, "usage_refresh_unsupported", "active usage refresh is available only for Claude, Codex, and Grok OAuth accounts")
		return
	}
	if !s.allowRate(fmt.Sprintf("usage-refresh:%d", account.ID), 6, time.Minute) {
		apiError(c, http.StatusTooManyRequests, "rate_limited", "account usage was refreshed too frequently")
		return
	}
	var (
		updated model.Subscription
		err     error
	)
	switch account.Provider {
	case model.ProviderClaude:
		updated, err = s.refreshClaudeQuota(c.Request.Context(), account)
	case model.ProviderCodex:
		updated, err = s.refreshCodexQuota(c.Request.Context(), account)
	default:
		updated, err = s.refreshGrokQuota(c.Request.Context(), account)
	}
	if err != nil {
		apiError(c, http.StatusBadGateway, "usage_refresh_failed", err.Error())
		return
	}
	usage, _ := s.repo.UsageSummary(c.Request.Context(), account.ID)
	c.JSON(http.StatusOK, subscriptionUsageResponse(updated, usage))
}

func (s *Server) refreshGrokQuota(ctx context.Context, account model.Subscription) (model.Subscription, error) {
	if account.Provider != model.ProviderGrok || account.AuthType != "oauth" {
		return account, errors.New("account is not a Grok OAuth account")
	}
	if strings.TrimSpace(s.cfg.Providers.GrokBilling) == "" {
		return account, errors.New("Grok billing endpoint is not configured")
	}

	checkedAt := time.Now().UTC()
	requestContext, cancel := context.WithTimeout(ctx, grokBillingTimeout)
	defer cancel()
	credentials, err := s.repo.Credentials(requestContext, account)
	if err == nil {
		credentials, err = s.grokCredentialsForRequest(requestContext, account, credentials, false)
	}
	if err != nil {
		return account, s.storeQuotaError(ctx, account, err)
	}
	profileTier := ""
	if credentials.UserID == "" {
		profile, profileErr := s.fetchGrokUser(requestContext, account, credentials)
		if profileErr == nil {
			credentials.UserID = profile.UserID
			credentials.Email = profile.Email
			profileTier = profile.SubscriptionTier
			if err := s.repo.UpdateSubscriptionCredentials(requestContext, account.ID, credentials, account.TokenExpiresAt); err != nil {
				return account, s.storeQuotaError(ctx, account, err)
			}
		} else if fallback := grokUserID(credentials); fallback != "" {
			credentials.UserID = fallback
		} else {
			return account, s.storeQuotaError(ctx, account, profileErr)
		}
	}

	body, status, err := s.fetchGrokBilling(requestContext, account, credentials)
	if err == nil && status == http.StatusUnauthorized && credentials.RefreshToken != "" {
		credentials, err = s.grokCredentialsForRequest(requestContext, account, credentials, true)
		if err == nil {
			body, status, err = s.fetchGrokBilling(requestContext, account, credentials)
		}
	}
	if err != nil {
		return account, s.storeQuotaError(ctx, account, err)
	}
	if status < 200 || status >= 300 {
		return account, s.storeQuotaError(ctx, account, fmt.Errorf("Grok billing endpoint returned HTTP %d", status))
	}
	quota, err := normalizeGrokBilling(account.Quota, body)
	if err != nil {
		return account, s.storeQuotaError(ctx, account, err)
	}
	if profileTier != "" {
		quota = quotaWithGrokTier(quota, profileTier)
	}
	if err := s.repo.UpdateSubscriptionQuota(ctx, account.ID, quota, checkedAt, nil); err != nil {
		return account, err
	}
	return s.repo.GetSubscription(ctx, account.ID)
}

type grokUserProfile struct {
	UserID           string `json:"userId"`
	Email            string `json:"email"`
	SubscriptionTier string `json:"subscriptionTier"`
}

func (s *Server) fetchGrokUser(ctx context.Context, account model.Subscription, credentials model.Credentials) (grokUserProfile, error) {
	endpoint, err := grokUserEndpoint(s.cfg.Providers.GrokBilling)
	if err != nil {
		return grokUserProfile{}, err
	}
	client, err := s.clientForSubscription(account)
	if err != nil {
		return grokUserProfile{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return grokUserProfile{}, err
	}
	request.Header.Set("Authorization", "Bearer "+credentials.Bearer())
	request.Header.Set("Accept", "application/json")
	applyGrokCLIIdentityHeaders(request.Header, s.cfg.GrokOAuth.ClientVersion, request.URL.String())
	// Billing/user probes always use the CLI token-auth surface, including test URLs.
	request.Header.Set("X-XAI-Token-Auth", "xai-grok-cli")
	response, err := doHTTP(client, request)
	if err != nil {
		return grokUserProfile{}, errors.New("could not query the Grok user profile")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return grokUserProfile{}, fmt.Errorf("Grok user endpoint returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxGrokBillingBodyBytes+1))
	if err != nil || len(body) > maxGrokBillingBodyBytes {
		return grokUserProfile{}, errors.New("could not read the Grok user profile")
	}
	var profile grokUserProfile
	if json.Unmarshal(body, &profile) != nil || cleanGrokIdentity(profile.UserID) == "" {
		return grokUserProfile{}, errors.New("Grok user profile did not include a user ID")
	}
	profile.UserID = cleanGrokIdentity(profile.UserID)
	profile.Email = strings.TrimSpace(profile.Email)
	profile.SubscriptionTier = strings.TrimSpace(profile.SubscriptionTier)
	return profile, nil
}

func grokUserEndpoint(billingEndpoint string) (string, error) {
	parsed, err := url.Parse(billingEndpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("Grok billing endpoint is invalid")
	}
	path := strings.TrimRight(parsed.Path, "/")
	separator := strings.LastIndex(path, "/")
	if separator < 0 {
		return "", errors.New("Grok billing endpoint path is invalid")
	}
	parsed.Path = path[:separator] + "/user"
	query := parsed.Query()
	query.Del("format")
	query.Set("include", "subscription")
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func quotaWithGrokTier(quota json.RawMessage, tier string) json.RawMessage {
	var value map[string]any
	if json.Unmarshal(quota, &value) != nil {
		return quota
	}
	if current, _ := value["subscription_tier"].(string); strings.TrimSpace(current) == "" {
		value["subscription_tier"] = tier
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return quota
	}
	return encoded
}

func (s *Server) storeQuotaError(ctx context.Context, account model.Subscription, cause error) error {
	message := strings.TrimSpace(cause.Error())
	if len(message) > 240 {
		message = message[:240]
	}
	_ = s.repo.UpdateSubscriptionQuotaError(ctx, account.ID, message)
	return errors.New(message)
}

func (s *Server) fetchGrokBilling(ctx context.Context, account model.Subscription, credentials model.Credentials) ([]byte, int, error) {
	client, err := s.clientForSubscription(account)
	if err != nil {
		return nil, 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.Providers.GrokBilling, nil)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Authorization", "Bearer "+credentials.Bearer())
	request.Header.Set("Accept", "application/json")
	applyGrokCLIIdentityHeaders(request.Header, s.cfg.GrokOAuth.ClientVersion, request.URL.String())
	// Billing/user probes always use the CLI token-auth surface, including test URLs.
	request.Header.Set("X-XAI-Token-Auth", "xai-grok-cli")
	if userID := grokUserID(credentials); userID != "" {
		request.Header.Set("x-userid", userID)
	}

	response, err := doHTTP(client, request)
	if err != nil {
		return nil, 0, errors.New("could not contact the Grok billing endpoint")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxGrokBillingBodyBytes+1))
	if err != nil {
		return nil, response.StatusCode, errors.New("could not read the Grok billing response")
	}
	if len(body) > maxGrokBillingBodyBytes {
		return nil, response.StatusCode, errors.New("Grok billing response is too large")
	}
	return body, response.StatusCode, nil
}

func normalizeGrokBilling(existing json.RawMessage, body []byte) (json.RawMessage, error) {
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	var payload any
	if err := decoder.Decode(&payload); err != nil {
		return nil, errors.New("Grok billing response is not valid JSON")
	}

	quota := map[string]any{}
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &quota)
	}
	found := false
	if value, ok := findGrokBillingValue(payload, "subscriptionTier", "subscription_tier", "planTier", "planName"); ok {
		if tier := grokString(value); tier != "" {
			quota["subscription_tier"] = tier
			found = true
		}
	}

	usedPercent, hasPercent := findGrokBillingNumber(payload, "creditUsagePercent", "credit_usage_percent", "usagePercent", "usedPercent")
	if !hasPercent {
		used, hasUsed := findGrokBillingNumber(payload, "used")
		limit, hasLimit := findGrokBillingNumber(payload, "monthlyLimit", "monthly_limit")
		if hasUsed && hasLimit && limit > 0 {
			usedPercent, hasPercent = used/limit*100, true
		}
	}
	resetAt, hasReset := findGrokBillingTime(payload, "billingPeriodEnd", "billing_period_end", "periodEnd", "period_end", "endTime", "end_time", "resetAt", "reset_at")
	periodType := "billing"
	var startAt string
	var hasStart bool
	if periodValue, ok := findGrokBillingValue(payload, "currentPeriod", "current_period"); ok {
		if period, valid := periodValue.(map[string]any); valid {
			if value, exists := directGrokValue(period, "type", "periodType", "period_type"); exists {
				periodType = normalizedGrokPeriod(grokString(value))
			}
			if value, exists := directGrokValue(period, "end"); exists {
				resetAt, hasReset = grokTime(value)
			}
			if value, exists := directGrokValue(period, "start"); exists {
				startAt, hasStart = grokTime(value)
			}
		}
	}
	if periodType == "billing" {
		if value, ok := findGrokBillingValue(payload, "periodType", "period_type", "timePeriod", "cadence", "duration"); ok {
			if candidate := strings.ToLower(grokString(value)); candidate != "" {
				periodType = normalizedGrokPeriod(candidate)
			}
		}
	}
	if hasPercent || hasReset {
		window := map[string]any{"name": periodType}
		if hasPercent {
			usedPercent = max(0, min(100, usedPercent))
			window["used_percent"] = usedPercent
			window["remaining_percent"] = 100 - usedPercent
		}
		if hasReset {
			window["reset_at"] = resetAt
		}
		if !hasStart {
			startAt, hasStart = findGrokBillingTime(payload, "billingPeriodStart", "billing_period_start", "periodStart", "period_start", "startTime", "start_time")
		}
		if hasStart {
			window["starts_at"] = startAt
		}
		quota["windows"] = []any{window}
		found = true
	}

	creditBalance := map[string]any{}
	if value, ok := findGrokBillingValue(payload, "prepaidBalance", "prepaid_balance", "prepaidCredits", "prepaid_credits"); ok {
		creditBalance["prepaid"] = compactGrokBalance(value)
	}
	if value, ok := findGrokBillingValue(payload, "onDemandBalance", "on_demand_balance", "onDemandCredits", "on_demand_credits"); ok {
		creditBalance["on_demand"] = compactGrokBalance(value)
	} else if capValue, hasCap := findGrokBillingNumber(payload, "onDemandCap", "on_demand_cap"); hasCap {
		usedValue, _ := findGrokBillingNumber(payload, "onDemandUsed", "on_demand_used")
		creditBalance["on_demand"] = map[string]any{
			"remaining": max(0, capValue-usedValue), "limit": capValue, "used": usedValue,
		}
	}
	if len(creditBalance) > 0 {
		quota["credit_balance"] = creditBalance
		found = true
	}
	if !found {
		return nil, errors.New("Grok billing response did not contain recognizable usage fields")
	}
	encoded, err := json.Marshal(quota)
	if err != nil {
		return nil, errors.New("could not encode Grok billing usage")
	}
	return encoded, nil
}

func grokUserID(credentials model.Credentials) string {
	if userID := cleanGrokIdentity(credentials.UserID); userID != "" {
		return userID
	}
	for _, token := range []string{credentials.IDToken, credentials.AccessToken} {
		parts := strings.Split(token, ".")
		if len(parts) < 2 || len(parts[1]) > 8192 {
			continue
		}
		payload, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			continue
		}
		var claims struct {
			Subject string `json:"sub"`
		}
		if json.Unmarshal(payload, &claims) == nil {
			if subject := cleanGrokIdentity(claims.Subject); subject != "" {
				return subject
			}
		}
	}
	return ""
}

func cleanGrokIdentity(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 256 || strings.ContainsAny(value, "\r\n") {
		return ""
	}
	return value
}

func directGrokValue(values map[string]any, names ...string) (any, bool) {
	wanted := map[string]bool{}
	for _, name := range names {
		wanted[canonicalGrokKey(name)] = true
	}
	for key, value := range values {
		if wanted[canonicalGrokKey(key)] {
			return value, true
		}
	}
	return nil, false
}

func normalizedGrokPeriod(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch {
	case strings.Contains(value, "week"):
		return "weekly"
	case strings.Contains(value, "month"):
		return "monthly"
	case strings.Contains(value, "day"):
		return "daily"
	default:
		return value
	}
}

func findGrokBillingValue(value any, names ...string) (any, bool) {
	wanted := map[string]bool{}
	for _, name := range names {
		wanted[canonicalGrokKey(name)] = true
	}
	var visit func(any, int) (any, bool)
	visit = func(current any, depth int) (any, bool) {
		if depth > 12 {
			return nil, false
		}
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				if wanted[canonicalGrokKey(key)] {
					return child, true
				}
			}
			for _, child := range typed {
				if found, ok := visit(child, depth+1); ok {
					return found, true
				}
			}
		case []any:
			for _, child := range typed {
				if found, ok := visit(child, depth+1); ok {
					return found, true
				}
			}
		}
		return nil, false
	}
	return visit(value, 0)
}

func canonicalGrokKey(value string) string {
	return strings.Map(func(character rune) rune {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			return unicode.ToLower(character)
		}
		return -1
	}, value)
}

func findGrokBillingNumber(value any, names ...string) (float64, bool) {
	found, ok := findGrokBillingValue(value, names...)
	if !ok {
		return 0, false
	}
	return grokNumber(found)
}

func grokNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case float64:
		return typed, true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(typed, "%")), 64)
		return parsed, err == nil
	case map[string]any:
		for _, key := range []string{"value", "val", "amount", "balance", "remaining", "used", "percent"} {
			if child, ok := typed[key]; ok {
				if parsed, valid := grokNumber(child); valid {
					return parsed, true
				}
			}
		}
	}
	return 0, false
}

func grokString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case map[string]any:
		for _, key := range []string{"name", "value", "label", "type"} {
			if child, ok := typed[key]; ok {
				if result := grokString(child); result != "" {
					return result
				}
			}
		}
	}
	return ""
}

func findGrokBillingTime(value any, names ...string) (string, bool) {
	found, ok := findGrokBillingValue(value, names...)
	if !ok {
		return "", false
	}
	return grokTime(found)
}

func grokTime(value any) (string, bool) {
	if number, ok := grokNumber(value); ok {
		if number > 1e12 {
			number /= 1000
		}
		return time.Unix(int64(number), 0).UTC().Format(time.RFC3339Nano), true
	}
	raw := grokString(value)
	if raw == "" {
		return "", false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.UTC().Format(time.RFC3339Nano), true
		}
	}
	return "", false
}

func compactGrokBalance(value any) any {
	input, ok := value.(map[string]any)
	if !ok {
		if number, valid := grokNumber(value); valid {
			return number
		}
		return grokString(value)
	}
	if raw, exists := directGrokValue(input, "val"); exists {
		if number, valid := grokNumber(raw); valid {
			return number
		}
	}
	output := map[string]any{}
	for _, key := range []string{"remaining", "balance", "limit", "total", "used", "currency", "unit"} {
		if child, exists := input[key]; exists {
			if number, valid := grokNumber(child); valid {
				output[key] = number
			} else if text := grokString(child); text != "" {
				output[key] = text
			}
		}
	}
	if len(output) == 0 {
		return grokString(value)
	}
	return output
}
