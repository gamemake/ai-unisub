package aiprovider2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
)

type Account struct {
	mu        sync.RWMutex
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
	config.CredentialID = strings.TrimSpace(config.CredentialID)
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
		return errors.New("account manager is required")
	}
	if config.Name == "" {
		return errors.New("account name is required")
	}
	if config.ProxyGroupID < 0 || config.MaxConcurrentConnections < 0 || config.QueueTimeoutSeconds < 0 {
		return errors.New("account limits and proxy group ID must not be negative")
	}
	switch config.ClientType {
	case "", ClientClaude, ClientCodex, ClientGrok:
	default:
		return errors.New("invalid client type")
	}
	if config.APIEndpoint != "" {
		endpoint, err := url.Parse(config.APIEndpoint)
		if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.Scheme != "http" && endpoint.Scheme != "https" {
			return errors.New("invalid API endpoint")
		}
	}

	switch config.Kind {
	case AccountGroup:
		if len(config.Members) == 0 {
			return errors.New("group account requires members")
		}
		if config.Supplier != "" || config.APIKey != "" || config.CredentialID != "" || config.APIEndpoint != "" || config.OfficialOnly {
			return errors.New("group account cannot own supplier credentials or endpoint")
		}
		seen := make(map[int]struct{}, len(config.Members))
		for _, member := range config.Members {
			if member.ID <= 0 || member.ID == a.ID || member.Weight <= 0 {
				return errors.New("group member ID and weight must be positive")
			}
			if _, ok := seen[member.ID]; ok {
				return errors.New("duplicate group member")
			}
			seen[member.ID] = struct{}{}
		}
		return nil
	case AccountOAuth:
		if config.CredentialID == "" {
			return errors.New("OAuth account credential_id is required")
		}
		if config.APIKey != "" {
			return errors.New("OAuth account cannot contain an API key")
		}
	case AccountAPI:
		if config.APIKey == "" {
			return errors.New("API account api_key is required")
		}
		if config.CredentialID != "" || config.OfficialOnly || config.SubscriptionPlan != "" {
			return errors.New("API account cannot contain subscription fields")
		}
	default:
		return errors.New("invalid account kind")
	}
	if len(config.Members) != 0 {
		return errors.New("only group accounts may contain members")
	}
	if config.Supplier == "" || a.manager.getSupplier(config.Supplier) == nil {
		return errors.New("unknown supplier")
	}
	return nil
}

func (a *Account) UpdateConfig(raw json.RawMessage) error {
	config, err := parseAccountConfig(raw)
	if err != nil {
		return err
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

func (a *Account) FetchQuota(ctx context.Context) (AccountQuota, error) {
	a.mu.RLock()
	config := a.Config
	a.mu.RUnlock()
	if config.Kind == AccountGroup {
		return AccountQuota{}, ErrQuotaUnsupported
	}

	supplier := a.manager.getSupplier(config.Supplier)
	if supplier == nil {
		return AccountQuota{}, errors.New("unknown supplier")
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
		return errors.New("unknown supplier")
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
