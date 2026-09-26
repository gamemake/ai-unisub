package aiprovider

import (
	"ai-unisub/internal/database"
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

type SupplierOpenAI struct{ SupplierData }

func newSupplierOpenAI(manager *providerManager) Supplier {
	return &SupplierOpenAI{
		manager: manager, id: "openai", name: "OpenAI",
		builtin: SupplierBuiltinConfig{
			OpenAIURL: "https://api.openai.com/v1",
			Models:    []string{"gpt-5", "gpt-5-mini", "gpt-4.1", "o3", "o4-mini"},
			Weights: []SubscriptionPlanWeight{
				{Name: "codex_plus", Weight: 1},
				{Name: "codex_pro_5x", Weight: 5},
				{Name: "codex_pro_20x", Weight: 20},
			},
		},
	}
}

func (s *SupplierOpenAI) RefreshModel(ctx context.Context, account *Account) ([]string, error) {
	if account == nil {
		return nil, errAccountRequired
	}
	config := s.accountConfig(account)
	base := config.APIEndpoint
	if base == "" {
		base = s.GetConfig().OpenAIURL
	}
	if base == "" {
		return nil, ErrModelsUnsupported
	}
	headers, err := s.authHeaders(config)
	if err != nil {
		return nil, err
	}
	body, err := s.doGET(ctx, account, strings.TrimRight(base, "/")+"/models", headers)
	if err != nil {
		return nil, err
	}
	return s.parseModels(body)
}

func (s *SupplierOpenAI) FetchQuota(ctx context.Context, account *Account) (AccountQuota, error) {
	if account == nil {
		return AccountQuota{}, errAccountRequired
	}
	config := s.accountConfig(account)
	if config.APIEndpoint != "" {
		return AccountQuota{}, ErrQuotaNotConfigured
	}
	if config.Kind != AccountSubscription {
		return AccountQuota{}, ErrQuotaUnsupported
	}
	headers, err := s.authHeaders(config)
	if err != nil {
		return AccountQuota{}, err
	}
	body, err := s.doGET(ctx, account, "https://chatgpt.com/backend-api/wham/usage", headers)
	if err != nil {
		return AccountQuota{}, err
	}
	observedAt := time.Now().UTC()
	windows, err := s.parseSubscriptionQuota(body, observedAt)
	if err != nil {
		return AccountQuota{}, err
	}
	return AccountQuota{Subscription: windows, CacheStatus: QuotaCacheFresh, UpdatedAt: observedAt}, nil
}

func (s *SupplierOpenAI) ResetQuota(ctx context.Context, account *Account, _ string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if account == nil {
		return errAccountRequired
	}
	return nil
}

func (s *SupplierOpenAI) accountConfig(account *Account) AccountConfig {
	account.mu.RLock()
	defer account.mu.RUnlock()
	config := account.Config
	config.Labels = slices.Clone(config.Labels)
	config.Members = slices.Clone(config.Members)
	return config
}

func (s *SupplierOpenAI) authHeaders(config AccountConfig) (http.Header, error) {
	headers := make(http.Header)
	headers.Set("Accept", "application/json")
	if config.Kind == AccountAPI {
		if config.APIKey == "" {
			return nil, ErrQuotaNotConfigured
		}
		headers.Set("Authorization", "Bearer "+config.APIKey)
		return headers, nil
	}
	token := config.Credential.AccessToken
	if token == "" {
		return nil, ErrQuotaNotConfigured
	}
	headers.Set("Authorization", "Bearer "+token)
	headers.Set("OpenAI-Beta", "codex-1")
	headers.Set("User-Agent", "codex-cli")
	if config.Credential.AccountID != "" {
		headers.Set("ChatGPT-Account-Id", config.Credential.AccountID)
	}
	return headers, nil
}

func (*SupplierOpenAI) parseModels(body []byte) ([]string, error) {
	var response struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}
	entries := response.Data
	if len(entries) == 0 {
		entries = response.Models
	}
	models := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, id)
	}
	if len(models) == 0 {
		return nil, ErrInvalidResponse
	}
	return models, nil
}

type codexRateLimitWindow struct {
	UsedPercent        *float64 `json:"used_percent"`
	LimitWindowSeconds int64    `json:"limit_window_seconds"`
	ResetAfterSeconds  *int64   `json:"reset_after_seconds"`
	ResetAt            int64    `json:"reset_at"`
}

type codexRateLimit struct {
	PrimaryWindow   *codexRateLimitWindow `json:"primary_window"`
	SecondaryWindow *codexRateLimitWindow `json:"secondary_window"`
}

// parseSubscriptionQuota projects the wham usage rate limits into standard subscription
// items. Windows are named by their actual duration rather than assuming primary is 5h;
// additional rate limits keep their limit name as a suffix.
func (*SupplierOpenAI) parseSubscriptionQuota(body []byte, observedAt time.Time) ([]SubscriptionQuotaItem, error) {
	var response struct {
		RateLimit            *codexRateLimit `json:"rate_limit"`
		AdditionalRateLimits []struct {
			LimitName      string          `json:"limit_name"`
			MeteredFeature string          `json:"metered_feature"`
			RateLimit      *codexRateLimit `json:"rate_limit"`
		} `json:"additional_rate_limits"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}
	windows := make([]SubscriptionQuotaItem, 0, 2)
	appendLimit := func(limit *codexRateLimit, suffix string) {
		if limit == nil {
			return
		}
		for _, window := range []*codexRateLimitWindow{limit.PrimaryWindow, limit.SecondaryWindow} {
			if window == nil || window.UsedPercent == nil {
				continue
			}
			item := SubscriptionQuotaItem{TimeDimension: codexWindowDimension(window.LimitWindowSeconds) + suffix, Usage: *window.UsedPercent}
			switch {
			case window.ResetAt > 0:
				item.ResetAt = time.Unix(window.ResetAt, 0).UTC()
			case window.ResetAfterSeconds != nil:
				item.ResetAt = observedAt.Add(time.Duration(*window.ResetAfterSeconds) * time.Second).UTC()
			}
			windows = append(windows, item)
		}
	}
	appendLimit(response.RateLimit, "")
	for _, additional := range response.AdditionalRateLimits {
		name := strings.TrimSpace(additional.LimitName)
		if name == "" {
			name = strings.TrimSpace(additional.MeteredFeature)
		}
		suffix := ""
		if name != "" {
			suffix = "_" + name
		}
		appendLimit(additional.RateLimit, suffix)
	}
	if len(windows) == 0 {
		return nil, ErrInvalidResponse
	}
	return windows, nil
}

func codexWindowDimension(seconds int64) string {
	switch {
	case seconds <= 0:
		return "unknown"
	case seconds == 7*24*3600:
		return "weekly"
	case seconds >= 28*24*3600 && seconds <= 31*24*3600:
		return "monthly"
	case seconds%(24*3600) == 0:
		return fmt.Sprintf("%dd", seconds/(24*3600))
	case seconds%3600 == 0:
		return fmt.Sprintf("%dh", seconds/3600)
	case seconds%60 == 0:
		return fmt.Sprintf("%dm", seconds/60)
	}
	return fmt.Sprintf("%ds", seconds)
}

func (s *SupplierOpenAI) recordCall(account *Account, req *http.Request, resp *http.Response, body []byte, requestErr error, started time.Time) error {
	config := s.accountConfig(account)
	trace := &database.PersistedCallTrace{
		AccountID: account.ID, AIProviderType: string(config.Kind), RequestMethod: req.Method,
		URL: req.URL.String(), OutboundURL: req.URL.String(),
		OriginalRequestHeaders: req.Header.Clone(), OutboundRequestHeaders: req.Header.Clone(),
		ResponseBody: slices.Clone(body), RequestDurationMs: time.Since(started).Milliseconds(), FinishedAt: time.Now().UTC(),
	}
	if resp != nil {
		trace.HTTPErrorCode = resp.StatusCode
		trace.ResponseHeaders = resp.Header.Clone()
	}
	if requestErr != nil {
		trace.HTTPErrorInfo = requestErr.Error()
	}
	return s.manager.db.RecordCallTrace(trace)
}
