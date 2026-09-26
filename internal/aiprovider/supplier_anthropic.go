package aiprovider

import (
	"ai-unisub/internal/database"
	"context"
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
	items, err := s.parseQuotaItems(body)
	if err != nil {
		return AccountQuota{}, err
	}
	return AccountQuota{Items: items, CacheStatus: QuotaCacheFresh, UpdatedAt: time.Now().UTC()}, nil
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
	if config.Kind == AccountAPI {
		if config.APIKey == "" {
			return nil, ErrQuotaNotConfigured
		}
		headers.Set("X-Api-Key", config.APIKey)
		headers.Set("Anthropic-Version", "2023-06-01")
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

func (s *SupplierAnthropic) parseQuotaItems(body []byte) ([]QuotaItem, error) {
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}
	items := make([]QuotaItem, 0)
	s.flattenQuota("", value, &items)
	if len(items) == 0 || len(items) > 128 {
		return nil, ErrInvalidResponse
	}
	slices.SortFunc(items, func(a, b QuotaItem) int { return strings.Compare(a.Name, b.Name) })
	return items, nil
}

func (s *SupplierAnthropic) flattenQuota(path string, value any, items *[]QuotaItem) {
	if len(*items) > 128 {
		return
	}
	switch value := value.(type) {
	case map[string]any:
		for _, key := range slices.Sorted(maps.Keys(value)) {
			next := key
			if path != "" {
				next = path + "." + key
			}
			s.flattenQuota(next, value[key], items)
		}
	case []any:
		for index, nested := range value {
			s.flattenQuota(fmt.Sprintf("%s[%d]", path, index), nested, items)
		}
	case nil:
	default:
		encoded, err := json.Marshal(value)
		if err == nil && path != "" {
			*items = append(*items, QuotaItem{Name: path, Value: string(encoded)})
		}
	}
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
