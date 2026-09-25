package aiprovider2

import (
	"ai-unisub/internal/oauth2"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	accessTokenRefreshInterval = 17 * time.Hour
	accessTokenRefreshAdvance  = 5 * time.Minute
)

type Account struct {
	mu        sync.RWMutex
	accessMu  sync.Mutex
	manager   *providerManager
	limiter   AccountLimiter // 超过最大连接数，需要排队。只对非 Group 类型有效
	scheduler GroupScheduler // 从子 Account 中选择一个。支队 Group 类型有效

	ID     int
	Config AccountConfig
	State  AccountState
	Quota  AccountQuota
	Dirty  bool // 修改了数据之后需要设置此标志，用来外部把修改落库

}

func parseAccountConfig(raw json.RawMessage) (AccountConfig, error) {
	var config AccountConfig
	if err := unmarshalObject(raw, &config); err != nil {
		return AccountConfig{}, fmt.Errorf("parse account config: %w", err)
	}
	config.Name = strings.TrimSpace(config.Name)
	config.Supplier = strings.ToLower(strings.TrimSpace(config.Supplier))
	config.APIEndpoint = strings.TrimSpace(config.APIEndpoint)
	config.APIKey = strings.TrimSpace(config.APIKey)
	config.Credential.AccessToken = strings.TrimSpace(config.Credential.AccessToken)
	config.Credential.RefreshToken = strings.TrimSpace(config.Credential.RefreshToken)
	return config, nil
}

func parseAccountState(raw json.RawMessage) (AccountState, error) {
	var state AccountState
	if err := unmarshalObject(raw, &state); err != nil {
		return AccountState{}, fmt.Errorf("parse account state: %w", err)
	}
	return state, nil
}

func parseAccountQuota(raw json.RawMessage) (AccountQuota, error) {
	var quota AccountQuota
	if err := unmarshalObject(raw, &quota); err != nil {
		return AccountQuota{}, fmt.Errorf("parse account quota: %w", err)
	}
	quota.Subscription = slices.Clone(quota.Subscription)
	quota.Items = slices.Clone(quota.Items)
	return quota, nil
}

func unmarshalObject(raw json.RawMessage, dst any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, dst)
}

func NewAccount(manager *providerManager, id int, configJson json.RawMessage) (*Account, error) {
	config, err := parseAccountConfig(configJson)
	if err != nil {
		return nil, err
	}

	account := &Account{
		manager: manager,
		ID:      id,
		Config:  config,
		State:   AccountState{},
		Quota:   AccountQuota{},
		Dirty:   true,
	}

	if err := account.checkAccountConfig(config); err != nil {
		return nil, err
	}
	account.initializeRuntime()

	return account, nil
}

func NewAccountFromDatabase(manager *providerManager, id int, configJson json.RawMessage, stateJson json.RawMessage, quotaJson json.RawMessage) (*Account, error) {
	config, err := parseAccountConfig(configJson)
	if err != nil {
		return nil, err
	}
	state, err := parseAccountState(stateJson)
	if err != nil {
		return nil, err
	}
	state.ActiveConnections = 0
	state.QueuedConnections = 0
	quota, err := parseAccountQuota(quotaJson)
	if err != nil {
		return nil, err
	}

	account := &Account{
		manager: manager,
		ID:      id,
		Config:  config,
		State:   state,
		Quota:   quota,
		Dirty:   false,
	}
	if err := account.checkAccountConfig(config); err != nil {
		return nil, err
	}
	account.initializeRuntime()

	return account, nil
}

func (a *Account) initializeRuntime() {
	if a.Config.Kind == AccountGroup {
		newGroupScheduler(a)
		return
	}
	newAccountLimiter(a)
}

func (a *Account) checkAccountConfig(config AccountConfig) error {
	if a == nil || a.manager == nil {
		return errAccountManagerRequired
	}
	if config.Name == "" {
		return errAccountNameRequired
	}
	if config.ProxyGroupID < 0 || config.MaxConcurrentConnections < 0 || config.QueueTimeoutSeconds < 0 {
		return errAccountLimitsOrProxyGroupNegative
	}
	switch config.ClientType {
	case "", ClientClaude, ClientCodex, ClientGrok:
	default:
		return errInvalidClientType
	}
	if config.APIEndpoint != "" {
		endpoint, err := url.Parse(config.APIEndpoint)
		if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.Scheme != "http" && endpoint.Scheme != "https" {
			return errInvalidAPIEndpoint
		}
	}

	switch config.Kind {
	case AccountGroup:
		if len(config.Members) == 0 {
			return errGroupAccountRequiresMembers
		}
		if config.Supplier != "" || config.APIKey != "" || config.Credential != (oauth2.OAuthCredential{}) || config.APIEndpoint != "" || config.OfficialOnly {
			return errGroupAccountHasSupplierConfiguration
		}
		seen := make(map[int]struct{}, len(config.Members))
		for _, member := range config.Members {
			if member.ID <= 0 || member.ID == a.ID || member.Weight <= 0 {
				return errGroupMemberIDOrWeightInvalid
			}
			if _, ok := seen[member.ID]; ok {
				return errDuplicateGroupMember
			}
			seen[member.ID] = struct{}{}
		}
		return nil
	case AccountOAuth:
		if config.Credential.AccessToken == "" {
			return errOAuthAccountAccessTokenRequired
		}
		if config.APIKey != "" {
			return errOAuthAccountHasAPIKey
		}
	case AccountAPI:
		if config.APIKey == "" {
			return errAPIAccountAPIKeyRequired
		}
		if config.Credential != (oauth2.OAuthCredential{}) || config.OfficialOnly || config.SubscriptionPlan != "" {
			return errAPIAccountHasSubscriptionFields
		}
	default:
		return errInvalidAccountKind
	}
	if len(config.Members) != 0 {
		return errOnlyGroupAccountsMayHaveMembers
	}
	if config.Supplier == "" || a.manager.getSupplier(config.Supplier) == nil {
		return errUnknownSupplier
	}
	return nil
}

func (a *Account) UpdateConfig(raw json.RawMessage) error {
	config, err := parseAccountConfig(raw)
	if err != nil {
		return err
	}

	a.accessMu.Lock()
	defer a.accessMu.Unlock()
	a.mu.RLock()
	currentCredential := a.Config.Credential
	a.mu.RUnlock()
	if config.Kind == AccountOAuth && config.Credential.RefreshToken != "" && config.Credential.RefreshToken == currentCredential.RefreshToken {
		config.Credential = currentCredential
	}
	if err := a.checkAccountConfig(config); err != nil {
		return err
	}
	a.mu.Lock()
	a.Config = config
	a.Dirty = true
	if a.limiter != nil {
		a.limiter.NotifyConfigChangedLocked()
	}
	if a.scheduler != nil {
		a.scheduler.NotifyConfigChangedLocked()
	}
	a.mu.Unlock()
	return nil
}

func (a *Account) close() {
	newAccountLimiter(a).Close()
}

// GetAccess resolves one concrete account for req and returns both values
// needed to forward it. API accounts use their API key as the access token;
// OAuth accounts refresh an expiring credential using ctx.
func (a *Account) GetAccess(ctx context.Context, req *http.Request) (accessToken, apiBaseURL string, err error) {
	if ctx == nil {
		return "", "", errContextRequired
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}

	account, err := a.accountForRequest(req)
	if err != nil {
		return "", "", err
	}

	account.mu.RLock()
	config := account.Config
	manager := account.manager
	account.mu.RUnlock()

	switch config.Kind {
	case AccountAPI:
		apiBaseURL, err = resolveAPIBaseURL(config, manager, req)
		if err != nil {
			return "", "", err
		}
		if config.APIKey == "" {
			return "", "", errAccountAPIKeyEmpty
		}
		return config.APIKey, apiBaseURL, nil
	case AccountOAuth:
	default:
		return "", "", errAccountDoesNotProvideAccessToken
	}

	account.accessMu.Lock()
	defer account.accessMu.Unlock()

	account.mu.RLock()
	config = account.Config
	manager = account.manager
	refreshAt := account.State.RefreshAt
	account.mu.RUnlock()
	apiBaseURL, err = resolveAPIBaseURL(config, manager, req)
	if err != nil {
		return "", "", err
	}
	credential := config.Credential
	if credential.AccessToken == "" {
		return "", "", errOAuthCredentialNoAccessToken
	}
	if !accessTokenNeedsRefresh(time.Now(), refreshAt, credential.ExpiresAt) {
		return credential.AccessToken, apiBaseURL, nil
	}
	if manager == nil || manager.oauth == nil {
		return "", "", errOAuthManagerNotConfigured
	}

	refreshed, err := manager.oauth.Refresh(ctx, config.Supplier, &credential, config.ProxyGroupID)
	if err != nil {
		return "", "", fmt.Errorf("refresh OAuth credential: %w", err)
	}
	account.mu.Lock()
	account.Config.Credential = *refreshed
	account.State.RefreshAt = time.Now().UTC()
	account.Dirty = true
	account.mu.Unlock()
	return refreshed.AccessToken, apiBaseURL, nil
}

func accessTokenNeedsRefresh(now, refreshAt, expiresAt time.Time) bool {
	if refreshAt.IsZero() || now.Sub(refreshAt) >= accessTokenRefreshInterval {
		return true
	}
	return !expiresAt.IsZero() && !expiresAt.After(now.Add(accessTokenRefreshAdvance))
}

func resolveAPIBaseURL(config AccountConfig, manager *providerManager, req *http.Request) (string, error) {
	if config.APIEndpoint != "" {
		return config.APIEndpoint, nil
	}
	if config.Kind == AccountOAuth && config.Supplier == "openai" && !isClaudeRequest(req) {
		return "https://chatgpt.com/backend-api/codex", nil
	}
	if manager == nil {
		return "", errAccountManagerNotConfigured
	}
	supplier := manager.getSupplier(config.Supplier)
	if supplier == nil {
		return "", errUnknownSupplier
	}

	supplierConfig := supplier.GetConfig()
	baseURL := supplierConfig.OpenAIURL
	if isClaudeRequest(req) {
		baseURL = supplierConfig.ClaudeURL
	}
	if baseURL == "" {
		return "", errSupplierNoAPIBaseURL
	}
	return baseURL, nil
}

func (a *Account) accountForRequest(req *http.Request) (*Account, error) {
	if a == nil {
		return nil, errAccountRequired
	}
	if req == nil {
		return nil, errRequestRequired
	}

	account := a
	visited := make(map[*Account]struct{})
	for {
		if _, exists := visited[account]; exists {
			return nil, errAccountGroupCycle
		}
		visited[account] = struct{}{}

		account.mu.RLock()
		kind := account.Config.Kind
		scheduler := account.scheduler
		account.mu.RUnlock()
		if kind != AccountGroup {
			return account, nil
		}
		if scheduler == nil {
			return nil, errAccountGroupSchedulerNotConfigured
		}
		account = scheduler.GetAccount(req)
		if account == nil {
			return nil, ErrUnavailable
		}
	}
}

func isClaudeRequest(req *http.Request) bool {
	return req.URL != nil && (strings.HasSuffix(req.URL.Path, "/messages") || strings.HasSuffix(req.URL.Path, "/messages/count_tokens"))
}

func (a *Account) FetchQuota(ctx context.Context) (AccountQuota, error) {
	a.mu.RLock()
	config := a.Config
	a.mu.RUnlock()
	if config.Kind == AccountGroup {
		return AccountQuota{}, ErrQuotaUnsupported
	}

	supplier := a.manager.getSupplier(config.Supplier)
	if supplier == nil {
		return AccountQuota{}, errUnknownSupplier
	}

	quota, err := supplier.FetchQuota(ctx, a)
	if err != nil {
		return AccountQuota{}, err
	}

	a.mu.Lock()
	a.Quota = cloneQuota(quota)
	a.Dirty = true
	a.mu.Unlock()
	return cloneQuota(quota), nil
}

func (a *Account) ResetQuota(ctx context.Context, resetType string) error {
	a.mu.RLock()
	config := a.Config
	a.mu.RUnlock()
	if config.Kind == AccountGroup {
		return ErrQuotaUnsupported
	}

	supplier := a.manager.getSupplier(config.Supplier)
	if supplier == nil {
		return errUnknownSupplier
	}

	if err := supplier.ResetQuota(ctx, a, resetType); err != nil {
		return err
	}

	a.mu.Lock()
	a.Quota = AccountQuota{CacheStatus: QuotaCacheMissing}
	a.Dirty = true
	a.mu.Unlock()
	return nil
}

func (a *Account) GetModels() []string {
	return a.getModels(make(map[*Account]struct{}))
}

func (a *Account) getModels(visited map[*Account]struct{}) []string {
	if a == nil || a.manager == nil {
		return nil
	}
	if _, exists := visited[a]; exists {
		return nil
	}
	visited[a] = struct{}{}
	defer delete(visited, a)

	a.mu.RLock()
	config := a.Config
	a.mu.RUnlock()
	if config.Kind == AccountGroup {
		memberModels := make([][]string, 0, len(config.Members))
		memberMappings := make([][]ModelMapping, 0, len(config.Members))
		for _, member := range config.Members {
			account := a.manager.getAccount(member.ID)
			if account == nil {
				return nil
			}
			account.mu.RLock()
			memberConfig := account.Config
			account.mu.RUnlock()
			if memberConfig.Kind == AccountGroup {
				memberModels = append(memberModels, account.getModels(visited))
				memberMappings = append(memberMappings, nil)
				continue
			}
			supplier := a.manager.getSupplier(memberConfig.Supplier)
			if supplier == nil {
				return nil
			}
			supplierConfig := supplier.GetConfig()
			memberModels = append(memberModels, supplierConfig.Models)
			memberMappings = append(memberMappings, supplierConfig.Mappings)
		}
		return intersectMappedModels(memberModels, memberMappings)
	}
	supplier := a.manager.getSupplier(config.Supplier)
	if supplier == nil {
		return []string{}
	}
	return slices.Clone(supplier.GetConfig().Models)
}

func intersectMappedModels(memberModels [][]string, memberMappings [][]ModelMapping) []string {
	if len(memberModels) == 0 || len(memberModels) != len(memberMappings) {
		return nil
	}
	candidates := make(map[string]struct{})
	for _, models := range memberModels {
		for _, model := range models {
			candidates[model] = struct{}{}
		}
	}
	for _, mappings := range memberMappings {
		for _, mapping := range mappings {
			if !strings.Contains(mapping.Pattern, "*") {
				candidates[mapping.Pattern] = struct{}{}
			}
		}
	}

	models := make([]string, 0, len(candidates))
	for candidate := range candidates {
		supported := true
		for index, available := range memberModels {
			mapped := MapModel(memberMappings[index], candidate)
			if !slices.Contains(available, mapped) {
				supported = false
				break
			}
		}
		if supported {
			models = append(models, candidate)
		}
	}
	slices.Sort(models)
	return models
}

func cloneQuota(quota AccountQuota) AccountQuota {
	quota.Subscription = slices.Clone(quota.Subscription)
	quota.Items = slices.Clone(quota.Items)
	return quota
}
