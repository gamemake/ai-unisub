package aiprovider

import (
	"ai-unisub/internal/database"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxSupplierResponseBytes = 1 << 20

type SubscriptionPlanWeight struct {
	Name   string `json:"name"`
	Weight int    `json:"weight"`
}

// ModelMapping maps a client-visible model name to a model supported by the
// supplier. Pattern may contain any number of '*' wildcards. Target is always
// a literal model name from the supplier's effective Models list.
type ModelMapping struct {
	Pattern string `json:"pattern"`
	Target  string `json:"target"`
}

type SupplierBuiltinConfig struct {
	ClaudeURL string
	OpenAIURL string
	Models    []string
	Mappings  []ModelMapping
	Weights   []SubscriptionPlanWeight
}

type SupplierOverlayConfig struct {
	Models   []string
	Mappings []ModelMapping
	Weights  []SubscriptionPlanWeight
}

type SupplierConfig = SupplierBuiltinConfig

type Supplier interface {
	GatewaySupplierListener

	GetID() string
	GetName() string
	SupportClients() []ClientType
	GetModels() []string

	GetBuiltinConfig() SupplierConfig                         // 获取代码中的内置的配置，用于恢复修改
	GetConfig() SupplierConfig                                // 获取实际的配置，包含修改和内置配置
	DiffConfig(SupplierConfig) (SupplierOverlayConfig, error) // 通过比较，计算出 SupplierOverlayConfig
	GetOverlayConfig() SupplierOverlayConfig
	SetOverlayConfig(overlay SupplierOverlayConfig, persist bool) error

	RefreshModel(ctx context.Context, account *Account) ([]string, error)
	FetchQuota(ctx context.Context, account *Account) (AccountQuota, error)
	ResetQuota(ctx context.Context, account *Account, resetType string) error
}

type SupplierData struct {
	manager *providerManager
	id      string
	name    string
	builtin SupplierBuiltinConfig // 代码中的内置的配置，用于恢复修改
	overlay SupplierOverlayConfig // 实际的配置，包含修改和内置配置
	mu      sync.RWMutex
}

func (s *SupplierData) GetAccess(ctx context.Context, account *Account, req *http.Request) (string, string, error) {
	if account == nil {
		return "", "", errAccountRequired
	}
	account.mu.RLock()
	supplierID := account.Config.Supplier
	account.mu.RUnlock()
	if supplierID != s.id {
		return "", "", errAccountSupplierMismatch
	}
	return account.GetAccess(ctx, req)
}

func (s *SupplierData) PreRequest(account *Account, req *http.Request, body *[]byte) error {
	if account == nil || req == nil || body == nil {
		return errAccountRequestAndBodyRequired
	}
	config := s.GetConfig()
	if len(config.Mappings) > 0 && len(*body) > 0 {
		var payload map[string]jsontext.Value
		if err := json.Unmarshal(*body, &payload); err != nil {
			return fmt.Errorf("parse request body for model mapping: %w", err)
		}
		var model string
		if rawModel, ok := payload["model"]; ok && json.Unmarshal(rawModel, &model) == nil {
			mapped := MapModel(config.Mappings, model)
			if mapped != model {
				mappedModel, err := json.Marshal(mapped)
				if err != nil {
					return fmt.Errorf("encode mapped model: %w", err)
				}
				payload["model"] = mappedModel
				mappedBody, err := json.Marshal(payload)
				if err != nil {
					return fmt.Errorf("encode mapped request body: %w", err)
				}
				*body = mappedBody
			}
		}
	}

	account.mu.RLock()
	accountConfig := account.Config
	account.mu.RUnlock()
	if isClaudeRequest(req) {
		if req.Header.Get("Anthropic-Version") == "" {
			req.Header.Set("Anthropic-Version", "2023-06-01")
		}
		if accountConfig.Kind == AccountSubscription && s.id == "anthropic" {
			// OAuth needs its own beta flag, but the client's betas must survive or
			// body fields such as context_management are rejected upstream.
			req.Header.Set("Anthropic-Beta", mergeAnthropicBeta(req.Header.Values("Anthropic-Beta"), "oauth-2025-04-20"))
			if req.Header.Get("User-Agent") == "" {
				req.Header.Set("User-Agent", "claude-code/2.1.7")
			}
		}
	} else if accountConfig.Kind == AccountSubscription && s.id == "openai" {
		req.Header.Set("OpenAI-Beta", "codex-1")
		req.Header.Set("User-Agent", "codex-cli")
		if accountConfig.Credential.AccountID != "" {
			req.Header.Set("ChatGPT-Account-Id", accountConfig.Credential.AccountID)
		}
	}
	return nil
}

func mergeAnthropicBeta(values []string, required ...string) string {
	var betas []string
	seen := make(map[string]struct{})
	for _, value := range append(values, required...) {
		for beta := range strings.SplitSeq(value, ",") {
			beta = strings.TrimSpace(beta)
			if _, ok := seen[beta]; beta == "" || ok {
				continue
			}
			seen[beta] = struct{}{}
			betas = append(betas, beta)
		}
	}
	return strings.Join(betas, ",")
}

func (s *SupplierData) DoRequest(ctx context.Context, account *Account, req *http.Request) (*http.Response, error) {
	if ctx == nil || account == nil || req == nil {
		return nil, errContextAccountAndRequestRequired
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	account.mu.RLock()
	proxyGroupID := account.Config.ProxyGroupID
	account.mu.RUnlock()
	if s.manager == nil || s.manager.proxy == nil {
		return nil, errSupplierProxyManagerNotConfigured
	}

	body, err := supplierRequestBody(req)
	if err != nil {
		return nil, err
	}
	var handle func(*http.Response) error
	if supplierRequestReplaySafe(req) {
		handle = func(response *http.Response) error {
			if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
				return fmt.Errorf("supplier upstream returned HTTP %d", response.StatusCode)
			}
			return nil
		}
	}
	response, requestErr := s.manager.proxy.Do(proxyGroupID, s.id, req.WithContext(ctx), body, handle)
	if response != nil {
		// proxy returns the final response together with the classifier error.
		// The Gateway must forward that upstream response to the client.
		return response, nil
	}
	return nil, requestErr
}

func supplierRequestBody(req *http.Request) ([]byte, error) {
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, fmt.Errorf("recreate request body: %w", err)
		}
		defer body.Close()
		return io.ReadAll(body)
	}
	if req.Body == nil || req.Body == http.NoBody {
		return nil, nil
	}
	return nil, errRequestBodyNotReplayable
}

func supplierRequestReplaySafe(req *http.Request) bool {
	if req.Header.Get("Idempotency-Key") != "" {
		return true
	}
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}

func (s *SupplierData) doGET(ctx context.Context, account *Account, endpoint string, headers http.Header) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if s.manager == nil || s.manager.proxy == nil || s.manager.db == nil {
		return nil, errSupplierManagerNotConfigured
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header = headers.Clone()
	started := time.Now()
	response, requestErr := s.DoRequest(ctx, account, req)
	if requestErr != nil {
		recordErr := s.recordCall(account, req, nil, nil, requestErr, started)
		return nil, errors.Join(ErrUpstream, requestErr, recordErr)
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxSupplierResponseBytes+1))
	readErr = errors.Join(readErr, response.Body.Close())
	recordErr := s.recordCall(account, req, response, body, readErr, started)
	if readErr != nil || len(body) > maxSupplierResponseBytes {
		return nil, errors.Join(ErrInvalidResponse, readErr, recordErr)
	}
	if recordErr != nil {
		return nil, recordErr
	}
	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, ErrAuthentication
	case http.StatusTooManyRequests:
		return nil, ErrRateLimited
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: HTTP %d", ErrUpstream, response.StatusCode)
	}
	return body, nil
}

func (s *SupplierData) recordCall(account *Account, req *http.Request, response *http.Response, body []byte, requestErr error, started time.Time) error {
	trace := &database.PersistedCallTrace{
		AccountID: account.ID, AIProviderType: s.id, RequestMethod: req.Method,
		URL: req.URL.String(), OutboundURL: req.URL.String(),
		OriginalRequestHeaders: req.Header.Clone(), OutboundRequestHeaders: req.Header.Clone(),
		ResponseBody: slices.Clone(body), RequestDurationMs: time.Since(started).Milliseconds(), FinishedAt: time.Now().UTC(),
	}
	if response != nil {
		trace.HTTPErrorCode = response.StatusCode
		trace.ResponseHeaders = response.Header.Clone()
	}
	if requestErr != nil {
		trace.HTTPErrorInfo = requestErr.Error()
	}
	return s.manager.db.RecordCallTrace(trace)
}

func (*SupplierData) PostResponse(account *Account, response *http.Response, _ []byte) error {
	if account == nil || response == nil {
		return errAccountAndResponseRequired
	}
	items := make([]QuotaItem, 0)
	for name, values := range response.Header {
		lower := strings.ToLower(name)
		if !strings.Contains(lower, "ratelimit") && !strings.Contains(lower, "rate-limit") {
			continue
		}
		for _, value := range values {
			items = append(items, QuotaItem{Name: name, Value: value, Source: "header"})
		}
	}
	if len(items) == 0 {
		return nil
	}
	slices.SortFunc(items, func(a, b QuotaItem) int {
		if result := strings.Compare(a.Name, b.Name); result != 0 {
			return result
		}
		return strings.Compare(a.Value, b.Value)
	})
	account.mu.Lock()
	account.Quota.Items = items
	account.Quota.CacheStatus = QuotaCacheFresh
	account.Quota.UpdatedAt = time.Now().UTC()
	account.Dirty = true
	account.mu.Unlock()
	return nil
}

// subscriptionQuotaStatus reports whether a response carries usable
// subscription quota headers: successful responses and rate-limit rejections.
func subscriptionQuotaStatus(statusCode int) bool {
	return statusCode >= 200 && statusCode < 300 || statusCode == http.StatusTooManyRequests
}

// singleHeader returns the trimmed header value only when it appears exactly
// once; missing, duplicated or blank values are rejected.
func singleHeader(header http.Header, name string) (string, bool) {
	values := header.Values(name)
	if len(values) != 1 {
		return "", false
	}
	value := strings.TrimSpace(values[0])
	return value, value != ""
}

func headerFloat(header http.Header, name string) (float64, bool) {
	value, ok := singleHeader(header, name)
	if !ok {
		return 0, false
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
		return 0, false
	}
	return number, true
}

func headerInt(header http.Header, name string) (int64, bool) {
	value, ok := singleHeader(header, name)
	if !ok {
		return 0, false
	}
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil || number < 0 {
		return 0, false
	}
	return number, true
}

// mergeSubscriptionQuota merges passively observed windows into the cached
// subscription quota by time dimension. Windows not observed are kept as is,
// and an observed window without a reset time keeps its cached reset time.
func mergeSubscriptionQuota(account *Account, windows []SubscriptionQuotaItem, observedAt time.Time) {
	if len(windows) == 0 {
		return
	}
	account.mu.Lock()
	defer account.mu.Unlock()
	merged := slices.Clone(account.Quota.Subscription)
	for _, window := range windows {
		index := slices.IndexFunc(merged, func(item SubscriptionQuotaItem) bool { return item.TimeDimension == window.TimeDimension })
		if index < 0 {
			merged = append(merged, window)
			continue
		}
		if window.ResetAt.IsZero() {
			window.ResetAt = merged[index].ResetAt
		}
		merged[index] = window
	}
	account.Quota.Subscription = merged
	account.Quota.CacheStatus = QuotaCacheFresh
	account.Quota.UpdatedAt = observedAt
	account.Dirty = true
}

func (s *SupplierData) GetID() string {
	return s.id
}

func (s *SupplierData) GetName() string {
	return s.name
}

func (s *SupplierData) SupportClients() []ClientType {
	var clients []ClientType
	if s.builtin.ClaudeURL != "" {
		clients = append(clients, ClientClaude)
	}
	if s.builtin.OpenAIURL != "" {
		clients = append(clients, ClientCodex, ClientGrok)
	}
	return clients
}

func (s *SupplierData) GetModels() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	models := s.overlay.Models
	if models == nil {
		models = s.builtin.Models
	}
	return slices.Clone(models)
}

func (s *SupplierData) GetBuiltinConfig() SupplierConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneSupplierConfig(s.builtin)
}

func (s *SupplierData) GetConfig() SupplierConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	models := s.overlay.Models
	if models == nil {
		models = s.builtin.Models
	}
	weights := s.overlay.Weights
	if weights == nil {
		weights = s.builtin.Weights
	}
	mappings := s.overlay.Mappings
	if mappings == nil {
		mappings = s.builtin.Mappings
	}
	config := SupplierConfig{
		ClaudeURL: s.builtin.ClaudeURL,
		OpenAIURL: s.builtin.OpenAIURL,
		Models:    slices.Clone(models),
		Mappings:  slices.Clone(mappings),
		Weights:   slices.Clone(weights),
	}
	return config
}

func (s *SupplierData) DiffConfig(config SupplierConfig) (SupplierOverlayConfig, error) {
	s.mu.RLock()
	builtin := cloneSupplierConfig(s.builtin)
	s.mu.RUnlock()
	if config.ClaudeURL != builtin.ClaudeURL || config.OpenAIURL != builtin.OpenAIURL {
		return SupplierOverlayConfig{}, errSupplierURLsImmutable
	}
	if err := validateSupplierValues(config.Models, config.Mappings, config.Weights); err != nil {
		return SupplierOverlayConfig{}, err
	}
	var overlay SupplierOverlayConfig
	if !slices.Equal(config.Models, builtin.Models) {
		overlay.Models = slices.Clone(config.Models)
	}
	if !slices.Equal(config.Mappings, builtin.Mappings) {
		overlay.Mappings = slices.Clone(config.Mappings)
	}
	if !slices.Equal(config.Weights, builtin.Weights) {
		overlay.Weights = slices.Clone(config.Weights)
	}
	return overlay, nil
}

func (s *SupplierData) GetOverlayConfig() SupplierOverlayConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneOverlay(s.overlay)
}

func (s *SupplierData) SetOverlayConfig(overlay SupplierOverlayConfig, persist bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	models := overlay.Models
	if models == nil {
		models = s.builtin.Models
	}
	mappings := overlay.Mappings
	if mappings == nil {
		mappings = s.builtin.Mappings
	}
	weights := overlay.Weights
	if weights == nil {
		weights = s.builtin.Weights
	}
	if err := validateSupplierValues(models, mappings, weights); err != nil {
		return err
	}

	if persist {
		if err := s.manager.saveSupplier(s.id, overlay); err != nil {
			return err
		}
	}

	s.overlay = overlay
	return nil
}

func cloneSupplierConfig(config SupplierConfig) SupplierConfig {
	config.Models = slices.Clone(config.Models)
	config.Mappings = slices.Clone(config.Mappings)
	config.Weights = slices.Clone(config.Weights)
	return config
}

func cloneOverlay(overlay SupplierOverlayConfig) SupplierOverlayConfig {
	overlay.Models = slices.Clone(overlay.Models)
	overlay.Mappings = slices.Clone(overlay.Mappings)
	overlay.Weights = slices.Clone(overlay.Weights)
	return overlay
}

func validateSupplierValues(models []string, mappings []ModelMapping, weights []SubscriptionPlanWeight) error {
	seenModels := make(map[string]struct{}, len(models))
	for _, model := range models {
		trimmed := strings.TrimSpace(model)
		if trimmed == "" || trimmed != model {
			return errSupplierModelEmpty
		}
		if _, ok := seenModels[model]; ok {
			return errDuplicateSupplierModel
		}
		seenModels[model] = struct{}{}
	}
	for _, mapping := range mappings {
		pattern := strings.TrimSpace(mapping.Pattern)
		target := strings.TrimSpace(mapping.Target)
		if pattern == "" || pattern != mapping.Pattern || target == "" || target != mapping.Target {
			return errModelMappingInvalid
		}
		if _, ok := seenModels[target]; !ok {
			return errModelMappingTargetInvalid
		}
	}
	seenWeights := make(map[string]struct{}, len(weights))
	for _, weight := range weights {
		if strings.TrimSpace(weight.Name) == "" || weight.Weight <= 0 {
			return errSubscriptionWeightInvalid
		}
		if _, ok := seenWeights[weight.Name]; ok {
			return errDuplicateSubscriptionWeight
		}
		seenWeights[weight.Name] = struct{}{}
	}
	return nil
}

// MapModel applies the first matching mapping and otherwise returns model
// unchanged.
func MapModel(mappings []ModelMapping, model string) string {
	for _, mapping := range mappings {
		if matchModelPattern(mapping.Pattern, model) {
			return mapping.Target
		}
	}
	return model
}

func matchModelPattern(pattern, model string) bool {
	patternIndex := 0
	modelIndex := 0
	starIndex := -1
	starMatch := 0

	for modelIndex < len(model) {
		if patternIndex < len(pattern) && pattern[patternIndex] == model[modelIndex] {
			patternIndex++
			modelIndex++
			continue
		}
		if patternIndex < len(pattern) && pattern[patternIndex] == '*' {
			starIndex = patternIndex
			patternIndex++
			starMatch = modelIndex
			continue
		}
		if starIndex < 0 {
			return false
		}
		patternIndex = starIndex + 1
		starMatch++
		modelIndex = starMatch
	}

	for patternIndex < len(pattern) && pattern[patternIndex] == '*' {
		patternIndex++
	}
	return patternIndex == len(pattern)
}
