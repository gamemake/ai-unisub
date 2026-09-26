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

type SupplierDeepSeek struct{ SupplierData }

func newSupplierDeepSeek(manager *providerManager) Supplier {
	return &SupplierDeepSeek{
		manager: manager, id: "deepseek", name: "DeepSeek",
		builtin: SupplierBuiltinConfig{
			ClaudeURL: "https://api.deepseek.com/anthropic/v1",
			OpenAIURL: "https://api.deepseek.com/v1",
			Models:    []string{"deepseek-chat", "deepseek-reasoner"},
		},
	}
}

func (s *SupplierDeepSeek) RefreshModel(ctx context.Context, account *Account) ([]string, error) {
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

func (s *SupplierDeepSeek) FetchQuota(ctx context.Context, account *Account) (AccountQuota, error) {
	if account == nil {
		return AccountQuota{}, errAccountRequired
	}
	config := s.accountConfig(account)
	if config.APIEndpoint != "" {
		return AccountQuota{}, ErrQuotaNotConfigured
	}
	if config.Kind != AccountAPI {
		return AccountQuota{}, ErrQuotaUnsupported
	}
	headers, err := s.authHeaders(config)
	if err != nil {
		return AccountQuota{}, err
	}
	body, err := s.doGET(ctx, account, "https://api.deepseek.com/user/balance", headers)
	if err != nil {
		return AccountQuota{}, err
	}
	items, err := s.parseQuotaItems(body)
	if err != nil {
		return AccountQuota{}, err
	}
	return AccountQuota{Items: items, CacheStatus: QuotaCacheFresh, UpdatedAt: time.Now().UTC()}, nil
}

func (s *SupplierDeepSeek) ResetQuota(ctx context.Context, account *Account, _ string) error {
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

func (s *SupplierDeepSeek) accountConfig(account *Account) AccountConfig {
	account.mu.RLock()
	defer account.mu.RUnlock()
	config := account.Config
	config.Labels = slices.Clone(config.Labels)
	config.Members = slices.Clone(config.Members)
	return config
}

func (s *SupplierDeepSeek) authHeaders(config AccountConfig) (http.Header, error) {
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
	return headers, nil
}

func (*SupplierDeepSeek) parseModels(body []byte) ([]string, error) {
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

func (s *SupplierDeepSeek) parseQuotaItems(body []byte) ([]QuotaItem, error) {
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

func (s *SupplierDeepSeek) flattenQuota(path string, value any, items *[]QuotaItem) {
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

func (s *SupplierDeepSeek) recordCall(account *Account, req *http.Request, resp *http.Response, body []byte, requestErr error, started time.Time) error {
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
