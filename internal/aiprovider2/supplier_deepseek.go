package aiprovider2

import (
	"ai-unisub/internal/database"
	"ai-unisub/internal/proxy"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
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

func (*SupplierDeepSeek) GetID() string   { return "deepseek" }
func (*SupplierDeepSeek) GetName() string { return "DeepSeek" }

func (s *SupplierDeepSeek) RefreshModel(ctx context.Context, account *Account) ([]string, error) {
	if account == nil {
		return nil, errors.New("account is required")
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
		return AccountQuota{}, errors.New("account is required")
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
		return errors.New("account is required")
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
	if config.CredentialID == "" || s.manager == nil || s.manager.db == nil {
		return nil, ErrQuotaNotConfigured
	}
	raw, err := s.manager.db.LoadCredential(config.CredentialID)
	if err != nil {
		return nil, fmt.Errorf("load OAuth credential: %w", err)
	}
	var credential map[string]any
	if err := json.Unmarshal(raw, &credential); err != nil {
		return nil, fmt.Errorf("parse OAuth credential: %w", err)
	}
	token := s.findString(credential, "access_token", "accessToken", "token")
	if token == "" {
		return nil, ErrQuotaNotConfigured
	}
	headers.Set("Authorization", "Bearer "+token)
	return headers, nil
}

func (s *SupplierDeepSeek) findString(value any, names ...string) string {
	switch value := value.(type) {
	case map[string]any:
		for _, name := range names {
			if candidate, ok := value[name].(string); ok && strings.TrimSpace(candidate) != "" {
				return strings.TrimSpace(candidate)
			}
		}
		for _, nested := range value {
			if candidate := s.findString(nested, names...); candidate != "" {
				return candidate
			}
		}
	case []any:
		for _, nested := range value {
			if candidate := s.findString(nested, names...); candidate != "" {
				return candidate
			}
		}
	}
	return ""
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

func (s *SupplierDeepSeek) doGET(ctx context.Context, account *Account, endpoint string, headers http.Header) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	config := s.accountConfig(account)
	manager := s.manager
	if manager == nil || manager.proxy == nil || manager.db == nil {
		return nil, errors.New("supplier manager is not configured")
	}
	tried := make([]string, 0)
	attempts := manager.proxy.ProxyRetryLimit(config.ProxyGroupID) + 1
	for attempt := range attempts {
		selected, err := manager.proxy.ResolveProxy(ctx, config.ProxyGroupID, "deepseek", tried)
		if err != nil {
			return nil, fmt.Errorf("resolve supplier proxy: %w", err)
		}
		if selected != nil {
			tried = append(tried, selected.String())
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			_ = manager.proxy.ReportProxyResult(selected, "deepseek", proxy.ApplicationIgnored, err, 0)
			return nil, err
		}
		req.Header = headers.Clone()
		client := proxy.Client(&http.Client{Timeout: 25 * time.Second}, selected)
		started := time.Now()
		resp, requestErr := client.Do(req)
		if requestErr != nil {
			class := proxy.NetworkError
			if ctx.Err() != nil {
				class = proxy.Canceled
			}
			_ = manager.proxy.ReportProxyResult(selected, "deepseek", class, requestErr, 0)
			recordErr := s.recordCall(account, req, nil, nil, requestErr, started)
			if ctx.Err() != nil {
				return nil, errors.Join(ctx.Err(), recordErr)
			}
			if attempt+1 < attempts {
				continue
			}
			return nil, errors.Join(ErrUpstream, requestErr, recordErr)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxSupplierResponseBytes+1))
		readErr = errors.Join(readErr, resp.Body.Close())
		class := proxy.Success
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			class = proxy.ApplicationIgnored
			if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
				class = proxy.ApplicationError
			}
		}
		_ = manager.proxy.ReportProxyResult(selected, "deepseek", class, readErr, resp.StatusCode)
		recordErr := s.recordCall(account, req, resp, body, readErr, started)
		if readErr != nil || len(body) > maxSupplierResponseBytes {
			return nil, errors.Join(ErrInvalidResponse, readErr, recordErr)
		}
		if recordErr != nil {
			return nil, recordErr
		}
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return nil, ErrAuthentication
		case http.StatusTooManyRequests:
			return nil, ErrRateLimited
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("%w: HTTP %d", ErrUpstream, resp.StatusCode)
		}
		return body, nil
	}
	return nil, ErrUpstream
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
