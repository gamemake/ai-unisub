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

type SupplierXAI struct{ SupplierData }

func newSupplierXAI(manager *providerManager) Supplier {
	return &SupplierXAI{
		manager: manager, id: "xai", name: "xAI",
		builtin: SupplierBuiltinConfig{
			OpenAIURL: "https://api.x.ai/v1",
			Models:    []string{"grok-4", "grok-3", "grok-3-mini"},
			Weights: []SubscriptionPlanWeight{
				{Name: "super_grok", Weight: 1},
				{Name: "super_grok_plus", Weight: 3},
				{Name: "super_grok_heavy", Weight: 10},
			},
		},
	}
}

func (s *SupplierXAI) RefreshModel(ctx context.Context, account *Account) ([]string, error) {
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

func (s *SupplierXAI) FetchQuota(ctx context.Context, account *Account) (AccountQuota, error) {
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
	body, err := s.doGET(ctx, account, "https://cli-chat-proxy.grok.com/v1/billing?format=credits", headers)
	if err != nil {
		return AccountQuota{}, err
	}
	items, err := s.parseQuotaItems(body)
	if err != nil {
		return AccountQuota{}, err
	}
	return AccountQuota{Items: items, CacheStatus: QuotaCacheFresh, UpdatedAt: time.Now().UTC()}, nil
}

func (s *SupplierXAI) ResetQuota(ctx context.Context, account *Account, _ string) error {
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

func (s *SupplierXAI) accountConfig(account *Account) AccountConfig {
	account.mu.RLock()
	defer account.mu.RUnlock()
	config := account.Config
	config.Labels = slices.Clone(config.Labels)
	config.Members = slices.Clone(config.Members)
	return config
}

func (s *SupplierXAI) authHeaders(config AccountConfig) (http.Header, error) {
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
	headers.Set("X-Xai-Token-Auth", "xai-grok-cli")
	headers.Set("User-Agent", "grok-shell/1.0.41")
	return headers, nil
}

func (*SupplierXAI) parseModels(body []byte) ([]string, error) {
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

func (s *SupplierXAI) parseQuotaItems(body []byte) ([]QuotaItem, error) {
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

func (s *SupplierXAI) flattenQuota(path string, value any, items *[]QuotaItem) {
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

func (s *SupplierXAI) recordCall(account *Account, req *http.Request, resp *http.Response, body []byte, requestErr error, started time.Time) error {
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
