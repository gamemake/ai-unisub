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
	"time"
)

type ClientType string

const (
	ClientClaude ClientType = "claude"
	ClientCodex  ClientType = "codex"
	ClientGrok   ClientType = "grok"
)

type AccountKind string

const (
	AccountOAuth AccountKind = "oauth"
	AccountAPI   AccountKind = "api"
	AccountGroup AccountKind = "group"
)

type GroupMember struct {
	ID     int `json:"id"`
	Weight int `json:"weight"`
}

type AccountConfig struct {
	// 公共部分
	Kind                     AccountKind `json:"kind,omitempty"`
	Name                     string      `json:"name"`
	Labels                   []string    `json:"labels"`
	Supplier                 string      `json:"supplier,omitempty"`
	ClientType               ClientType  `json:"client_type,omitempty"`
	ProxyGroupID             int         `json:"proxy_group_id,omitempty"`
	Enabled                  bool        `json:"enabled"`
	MaxConcurrentConnections int         `json:"max_concurrent_connections"`
	QueueTimeoutSeconds      int         `json:"queue_timeout_seconds"`

	// 订阅独有
	SubscriptionPlan string `json:"subscription_plan,omitempty"`
	OfficialOnly     bool   `json:"official_only,omitzero"`
	CredentialID     string `json:"credential_id,omitempty"`

	// API 独有
	APIEndpoint string `json:"api_endpoint"`
	APIKey      string `json:"api_key,omitempty"`

	// Group 独有
	Members []GroupMember `json:"members,omitempty"`
}

type AccountState struct{}

type AccountQuota struct {
	Subscription []SubscriptionQuotaItem `json:"subscription,omitempty"`
	Items        []QuotaItem             `json:"items,omitempty"`
	CacheStatus  QuotaCacheStatus        `json:"cache_status,omitempty"`
	UpdatedAt    time.Time               `json:"updated_at,omitzero"`
}

type QuotaItem struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Source string `json:"source,omitempty"`
}

type SubscriptionQuotaItem struct {
	TimeDimension string    `json:"time_dimension"`
	Usage         float64   `json:"usage"`
	ResetAt       time.Time `json:"reset_at"`
}

type QuotaCacheStatus string

const (
	QuotaCacheMissing QuotaCacheStatus = "missing"
	QuotaCacheFresh   QuotaCacheStatus = "fresh"
)

type Account struct {
	Manager *Manager
	ID      int
	Config  AccountConfig
	State   AccountState
	Quota   AccountQuota
	Dirty   bool // 修改了数据之后需要设置此标志，用来外部把修改落库

	mu sync.RWMutex
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

func NewAccount(manager *Manager, id int, configJson json.RawMessage) (*Account, error) {
	config, err := parseAccountConfig(configJson)
	if err != nil {
		return nil, err
	}

	account := &Account{
		Manager: manager,
		ID:      id,
		Config:  config,
		State:   AccountState{},
		Quota:   AccountQuota{},
		Dirty:   true,
	}

	if err := account.checkAccountConfig(config); err != nil {
		return nil, err
	}

	return account, nil
}

func NewAccountFromDatabase(manager *Manager, id int, configJson json.RawMessage, stateJson json.RawMessage, quotaJson json.RawMessage) (*Account, error) {
	config, err := parseAccountConfig(configJson)
	if err != nil {
		return nil, err
	}
	state, err := parseAccountState(stateJson)
	if err != nil {
		return nil, err
	}
	quota, err := parseAccountQuota(quotaJson)
	if err != nil {
		return nil, err
	}

	account := &Account{
		Manager: manager,
		ID:      id,
		Config:  config,
		State:   state,
		Quota:   quota,
		Dirty:   false,
	}
	if err := account.checkAccountConfig(config); err != nil {
		return nil, err
	}

	return account, nil
}

func (a *Account) checkAccountConfig(config AccountConfig) error {
	if a == nil || a.Manager == nil {
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
	if config.Supplier == "" || a.Manager.getSupplier(config.Supplier) == nil {
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
	a.mu.Unlock()
	return nil
}

func (a *Account) FetchQuota(ctx context.Context) (AccountQuota, error) {
	a.mu.RLock()
	config := a.Config
	a.mu.RUnlock()
	if config.Kind == AccountGroup {
		return AccountQuota{}, ErrQuotaUnsupported
	}

	supplier := a.Manager.getSupplier(config.Supplier)
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

	supplier := a.Manager.getSupplier(config.Supplier)
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
	if a == nil || a.Manager == nil {
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
			account := a.Manager.getAccount(member.ID)
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
			supplier := a.Manager.getSupplier(memberConfig.Supplier)
			if supplier == nil {
				return nil
			}
			supplierConfig := supplier.GetConfig()
			memberModels = append(memberModels, supplierConfig.Models)
			memberMappings = append(memberMappings, supplierConfig.Mappings)
		}
		return intersectMappedModels(memberModels, memberMappings)
	}
	supplier := a.Manager.getSupplier(config.Supplier)
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
