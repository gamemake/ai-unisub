package aiprovider

import (
	"ai-unisub/internal/database"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"
)

type SupplierAnthropic struct{ SupplierData }

func newSupplierAnthropic(manager *providerManager) Supplier {
	return &SupplierAnthropic{
		manager: manager, id: "anthropic", name: "Anthropic",
		builtin: SupplierBuiltinConfig{
			ClaudeURL: "https://api.anthropic.com/v1",
			Models:    []string{"claude-opus-4-6", "claude-sonnet-4-6", "claude-haiku-4-5-20251001"},
			Weights: []SubscriptionPlanWeight{
				{Name: "claude_pro", Weight: 1},
				{Name: "claude_max_5x", Weight: 5},
				{Name: "claude_max_20x", Weight: 20},
			},
		},
	}
}

func (s *SupplierAnthropic) RefreshModel(ctx context.Context, account *Account) ([]string, error) {
	if account == nil {
		return nil, errAccountRequired
	}
	config := s.accountConfig(account)
	base := config.APIEndpoint
	if base == "" {
		base = s.GetConfig().ClaudeURL
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

func (s *SupplierAnthropic) FetchQuota(ctx context.Context, account *Account) (AccountQuota, error) {
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
	body, err := s.doGET(ctx, account, "https://api.anthropic.com/api/oauth/usage", headers)
	if err != nil {
		return AccountQuota{}, err
	}
	windows, err := s.parseSubscriptionQuota(body)
	if err != nil {
		return AccountQuota{}, err
	}
	return AccountQuota{Subscription: windows, CacheStatus: QuotaCacheFresh, UpdatedAt: time.Now().UTC()}, nil
}

func (s *SupplierAnthropic) ResetQuota(ctx context.Context, account *Account, _ string) error {
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

func (s *SupplierAnthropic) accountConfig(account *Account) AccountConfig {
	account.mu.RLock()
	defer account.mu.RUnlock()
	config := account.Config
	config.Labels = slices.Clone(config.Labels)
	config.Members = slices.Clone(config.Members)
	return config
}

func (s *SupplierAnthropic) authHeaders(config AccountConfig) (http.Header, error) {
	headers := make(http.Header)
	headers.Set("Accept", "application/json")
	headers.Set("Anthropic-Version", "2023-06-01")
	if config.Kind == AccountAPI {
		if config.APIKey == "" {
			return nil, ErrQuotaNotConfigured
		}
		headers.Set("X-Api-Key", config.APIKey)
		return headers, nil
	}
	token := config.Credential.AccessToken
	if token == "" {
		return nil, ErrQuotaNotConfigured
	}
	headers.Set("Authorization", "Bearer "+token)
	headers.Set("Anthropic-Beta", "oauth-2025-04-20")
	headers.Set("User-Agent", "claude-code/2.1.7")
	return headers, nil
}

func (*SupplierAnthropic) parseModels(body []byte) ([]string, error) {
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

// parseSubscriptionQuota projects the OAuth usage windows (five_hour, seven_day and
// seven_day_<scope>) into standard subscription items. Windows whose utilization is
// null are skipped; utilization is already a percentage.
func (*SupplierAnthropic) parseSubscriptionQuota(body []byte) ([]SubscriptionQuotaItem, error) {
	var response map[string]jsontext.Value
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}
	windows := make([]SubscriptionQuotaItem, 0, 2)
	for _, key := range slices.Sorted(maps.Keys(response)) {
		var dimension string
		switch {
		case key == "five_hour":
			dimension = "5h"
		case key == "seven_day":
			dimension = "weekly"
		case strings.HasPrefix(key, "seven_day_"):
			dimension = "weekly_" + strings.TrimPrefix(key, "seven_day_")
		default:
			continue
		}
		var window struct {
			Utilization *float64 `json:"utilization"`
			ResetsAt    *string  `json:"resets_at"`
		}
		if err := json.Unmarshal(response[key], &window); err != nil || window.Utilization == nil {
			continue
		}
		item := SubscriptionQuotaItem{TimeDimension: dimension, Usage: *window.Utilization}
		if window.ResetsAt != nil {
			if resetAt, err := time.Parse(time.RFC3339Nano, *window.ResetsAt); err == nil {
				item.ResetAt = resetAt.UTC()
			}
		}
		windows = append(windows, item)
	}
	if len(windows) == 0 {
		return nil, ErrInvalidResponse
	}
	slices.SortStableFunc(windows, func(a, b SubscriptionQuotaItem) int {
		return anthropicWindowRank(a.TimeDimension) - anthropicWindowRank(b.TimeDimension)
	})
	return windows, nil
}

const anthropicUnifiedHeaderPrefix = "anthropic-ratelimit-unified-"

// PostResponse refreshes subscription windows from the unified rate-limit
// headers of a model response; API accounts keep the generic header capture.
func (s *SupplierAnthropic) PostResponse(account *Account, response *http.Response, body []byte) error {
	if account == nil || response == nil {
		return errAccountAndResponseRequired
	}
	if s.accountConfig(account).Kind != AccountSubscription {
		return s.SupplierData.PostResponse(account, response, body)
	}
	if !subscriptionQuotaStatus(response.StatusCode) {
		return nil
	}
	mergeSubscriptionQuota(account, s.parseSubscriptionHeaders(response.Header), time.Now().UTC())
	return nil
}

// parseSubscriptionHeaders projects anthropic-ratelimit-unified-<claim>-utilization
// and -reset headers into subscription items. Header utilization is a ratio
// (0.25 = 25%) and reset is a Unix timestamp; unknown claims are skipped.
func (*SupplierAnthropic) parseSubscriptionHeaders(header http.Header) []SubscriptionQuotaItem {
	var windows []SubscriptionQuotaItem
	for name := range header {
		lower := strings.ToLower(name)
		claim, ok := strings.CutPrefix(lower, anthropicUnifiedHeaderPrefix)
		if !ok {
			continue
		}
		claim, ok = strings.CutSuffix(claim, "-utilization")
		if !ok {
			continue
		}
		dimension := anthropicClaimDimension(claim)
		if dimension == "" {
			continue
		}
		utilization, ok := headerFloat(header, name)
		if !ok {
			continue
		}
		item := SubscriptionQuotaItem{TimeDimension: dimension, Usage: utilization * 100}
		if reset, ok := headerInt(header, anthropicUnifiedHeaderPrefix+claim+"-reset"); ok && reset > 0 {
			if reset > 1e12 {
				item.ResetAt = time.UnixMilli(reset).UTC()
			} else {
				item.ResetAt = time.Unix(reset, 0).UTC()
			}
		}
		windows = append(windows, item)
	}
	slices.SortFunc(windows, func(a, b SubscriptionQuotaItem) int {
		if rank := anthropicWindowRank(a.TimeDimension) - anthropicWindowRank(b.TimeDimension); rank != 0 {
			return rank
		}
		return strings.Compare(a.TimeDimension, b.TimeDimension)
	})
	return windows
}

// anthropicClaimDimension maps a unified header claim to the time dimension
// used by the OAuth usage body, so passive and active results merge.
func anthropicClaimDimension(claim string) string {
	switch claim {
	case "5h":
		return "5h"
	case "7d":
		return "weekly"
	case "7d_oi":
		return "weekly_overage_included"
	}
	if scope, ok := strings.CutPrefix(claim, "7d_"); ok && scope != "" {
		return "weekly_" + scope
	}
	return ""
}

func anthropicWindowRank(dimension string) int {
	switch dimension {
	case "5h":
		return 0
	case "weekly":
		return 1
	}
	return 2
}

func (s *SupplierAnthropic) recordCall(account *Account, req *http.Request, resp *http.Response, body []byte, requestErr error, started time.Time) error {
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
