package database

import (
	"cmp"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"sync"
	"time"
)

// MemoryDatabase is the Database implementation. It keeps the small, frequently
// accessed records (accounts, users, API keys, proxy groups, configs, and
// credentials) in process memory, owns input validation, and
// delegates durable storage, call traces, proxy logs, and usage aggregation to
// a SQLite or PostgreSQL store.
//
// Reads of cached records never reach the store. Writes reach the store first
// and update the cache only after the store accepted the change, so the cache
// never holds data that was not persisted.
type MemoryDatabase struct {
	mu     sync.RWMutex
	store  Database
	opened bool

	credentials map[string]json.RawMessage
	accounts    map[int]PersistedAccount
	users       map[int]PersistedUser
	apiKeys     map[int]PersistedAPIKey
	proxyGroups map[int]PersistedProxyGroup
	configs     map[int]PersistedConfig
}

// newMemoryDatabase wraps a durable store (SQLiteDatabase or PostgreSQLDatabase)
// in the caching Database implementation.
func newMemoryDatabase(store Database) *MemoryDatabase {
	return &MemoryDatabase{
		store:       store,
		credentials: make(map[string]json.RawMessage),
		accounts:    make(map[int]PersistedAccount),
		users:       make(map[int]PersistedUser),
		apiKeys:     make(map[int]PersistedAPIKey),
		proxyGroups: make(map[int]PersistedProxyGroup),
		configs:     make(map[int]PersistedConfig),
	}
}

// Open opens the store and loads the cached records through the Database
// interface it wraps.
func (m *MemoryDatabase) Open() error {
	if err := m.store.Open(); err != nil {
		return err
	}
	accounts, err := m.store.ListAccounts()
	if err != nil {
		return err
	}
	users, err := m.store.ListUsers()
	if err != nil {
		return err
	}
	apiKeys, err := m.store.ListAPIKeys(0)
	if err != nil {
		return err
	}
	proxyGroups, err := m.store.ListProxyGroups()
	if err != nil {
		return err
	}
	configs, err := m.store.ListConfigs()
	if err != nil {
		return err
	}
	m.replaceCache(accounts, users, apiKeys, proxyGroups, configs)
	return nil
}

// Close closes the store. Cached records stay in memory; reads report the
// database as closed until the next Open.
func (m *MemoryDatabase) Close() error {
	m.mu.Lock()
	m.opened = false
	m.mu.Unlock()
	return m.store.Close()
}

// ensureOpen reports whether Open succeeded and Close has not been called.
func (m *MemoryDatabase) ensureOpen() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.opened {
		return errors.New("database is not open")
	}
	return nil
}

func (m *MemoryDatabase) ListConfigs() ([]PersistedConfig, error) {
	if err := m.ensureOpen(); err != nil {
		return nil, err
	}
	return m.cachedConfigs(), nil
}

func (m *MemoryDatabase) ListConfigsByType(configType string) ([]PersistedConfig, error) {
	if err := validateConfigType(configType); err != nil {
		return nil, err
	}
	if err := m.ensureOpen(); err != nil {
		return nil, err
	}
	return m.cachedConfigsByType(configType), nil
}

func (m *MemoryDatabase) LoadConfig(configType, name string) (PersistedConfig, error) {
	if err := validateConfigType(configType); err != nil {
		return PersistedConfig{}, err
	}
	if err := validateConfigName(name); err != nil {
		return PersistedConfig{}, err
	}
	if err := m.ensureOpen(); err != nil {
		return PersistedConfig{}, err
	}
	value, ok := m.cachedConfigByTypeName(configType, name)
	if !ok {
		return PersistedConfig{}, nil
	}
	return value, nil
}

func (m *MemoryDatabase) SaveConfig(value *PersistedConfig) error {
	if err := validateConfig(value); err != nil {
		return err
	}
	if err := m.ensureOpen(); err != nil {
		return err
	}
	if value.ID > 0 {
		existing, ok := m.cachedConfig(value.ID)
		if !ok {
			return errors.New("config not found")
		}
		if existing.Type != value.Type || existing.Name != value.Name {
			return errors.New("config type and name cannot be changed")
		}
	}
	if err := m.store.SaveConfig(value); err != nil {
		return err
	}
	m.cacheConfig(*value)
	return nil
}

func (m *MemoryDatabase) DeleteConfig(id int) error {
	if err := m.ensureOpen(); err != nil {
		return err
	}
	if err := m.store.DeleteConfig(id); err != nil {
		return err
	}
	m.dropConfig(id)
	return nil
}

func (m *MemoryDatabase) ListProxyGroups() ([]PersistedProxyGroup, error) {
	if err := m.ensureOpen(); err != nil {
		return nil, err
	}
	return m.cachedProxyGroups(), nil
}

func (m *MemoryDatabase) SaveProxyGroup(value *PersistedProxyGroup) error {
	if value == nil || value.ID < 0 {
		return errors.New("proxy group and ID are required")
	}
	if err := m.ensureOpen(); err != nil {
		return err
	}
	if err := m.store.SaveProxyGroup(value); err != nil {
		return err
	}
	m.cacheProxyGroup(*value)
	return nil
}

func (m *MemoryDatabase) DeleteProxyGroup(id int) error {
	if err := m.ensureOpen(); err != nil {
		return err
	}
	if err := m.store.DeleteProxyGroup(id); err != nil {
		return err
	}
	m.dropProxyGroup(id)
	return nil
}

func (m *MemoryDatabase) RecordProxyLog(value *PersistedProxyLog) error {
	if value == nil {
		return errors.New("proxy log is nil")
	}
	if err := m.ensureOpen(); err != nil {
		return err
	}
	return m.store.RecordProxyLog(value)
}

func (m *MemoryDatabase) QueryProxyLogs(filter ProxyLogFilter, page, pageSize int) ([]PersistedProxyLog, int, error) {
	if err := m.ensureOpen(); err != nil {
		return nil, 0, err
	}
	if page < 1 {
		return nil, 0, errors.New("page must be greater than zero")
	}
	if pageSize < 1 {
		return nil, 0, errors.New("page size must be greater than zero")
	}
	if err := validateTimeRange(filter.TimeRange); err != nil {
		return nil, 0, err
	}
	return m.store.QueryProxyLogs(filter, page, pageSize)
}

func (m *MemoryDatabase) CleanupProxyLog(days int) error {
	if days < 0 {
		return errors.New("cleanup days must not be negative")
	}
	if err := m.ensureOpen(); err != nil {
		return err
	}
	return m.store.CleanupProxyLog(days)
}

func (m *MemoryDatabase) LoadCredential(id string) (json.RawMessage, error) {
	if err := m.ensureOpen(); err != nil {
		return nil, err
	}
	if raw := m.cachedCredential(id); raw != nil {
		return raw, nil
	}
	raw, err := m.store.LoadCredential(id)
	if err != nil {
		return nil, err
	}
	if !json.Valid(raw) {
		_ = m.DeleteCredential(id)
		return nil, errors.New("stored credential is invalid JSON")
	}
	m.cacheCredential(id, raw)
	return raw, nil
}

func (m *MemoryDatabase) SaveCredential(id string, value json.RawMessage) error {
	if id == "" || len(value) == 0 || !json.Valid(value) {
		return errors.New("credential ID and credential are required")
	}
	if err := m.ensureOpen(); err != nil {
		return err
	}
	if err := m.store.SaveCredential(id, value); err != nil {
		return err
	}
	m.cacheCredential(id, value)
	return nil
}

func (m *MemoryDatabase) DeleteCredential(id string) error {
	if err := m.ensureOpen(); err != nil {
		return err
	}
	if err := m.store.DeleteCredential(id); err != nil {
		return err
	}
	m.dropCredential(id)
	return nil
}

func (m *MemoryDatabase) ListAccounts() ([]PersistedAccount, error) {
	if err := m.ensureOpen(); err != nil {
		return nil, err
	}
	return m.cachedAccounts(), nil
}

func (m *MemoryDatabase) SaveAccount(value *PersistedAccount) error {
	if err := validateAccount(value); err != nil {
		return err
	}
	if err := m.ensureOpen(); err != nil {
		return err
	}
	if value.ID > 0 && !m.hasAccount(value.ID) {
		return errors.New("account not existed")
	}
	if err := m.store.SaveAccount(value); err != nil {
		return err
	}
	m.cacheAccount(*value)
	return nil
}

func (m *MemoryDatabase) DeleteAccount(id int) error {
	if err := m.ensureOpen(); err != nil {
		return err
	}
	if err := m.store.DeleteAccount(id); err != nil {
		return err
	}
	m.dropAccount(id)
	return nil
}

func (m *MemoryDatabase) ListUsers() ([]PersistedUser, error) {
	if err := m.ensureOpen(); err != nil {
		return nil, err
	}
	return m.cachedUsers(), nil
}

func (m *MemoryDatabase) SaveUser(value *PersistedUser) error {
	if err := validateUser(value); err != nil {
		return err
	}
	if err := m.ensureOpen(); err != nil {
		return err
	}
	if value.ID > 0 && !m.hasUser(value.ID) {
		return errors.New("user not found")
	}
	if !value.Enabled && value.UpdatedAt.IsZero() {
		value.Enabled = true
	}
	if err := m.store.SaveUser(value); err != nil {
		return err
	}
	m.cacheUser(*value)
	return nil
}

func (m *MemoryDatabase) DeleteUser(id int) error {
	if err := m.ensureOpen(); err != nil {
		return err
	}
	if err := m.store.DeleteUser(id); err != nil {
		return err
	}
	m.dropUser(id)
	return nil
}

func (m *MemoryDatabase) ListAPIKeys(userID int) ([]PersistedAPIKey, error) {
	if err := m.ensureOpen(); err != nil {
		return nil, err
	}
	return m.cachedAPIKeys(userID), nil
}

func (m *MemoryDatabase) SaveAPIKey(value *PersistedAPIKey) error {
	if err := validateAPIKey(value); err != nil {
		return err
	}
	if err := m.ensureOpen(); err != nil {
		return err
	}
	if value.ID > 0 && !m.hasAPIKey(value.ID) {
		return errors.New("apikey not found")
	}
	if err := m.store.SaveAPIKey(value); err != nil {
		return err
	}
	m.cacheAPIKey(*value)
	return nil
}

func (m *MemoryDatabase) DeleteAPIKey(id int) error {
	if err := m.ensureOpen(); err != nil {
		return err
	}
	if err := m.store.DeleteAPIKey(id); err != nil {
		return err
	}
	m.dropAPIKey(id)
	return nil
}

func (m *MemoryDatabase) RecordCallTrace(trace *PersistedCallTrace) error {
	if trace == nil {
		return errors.New("call trace is nil")
	}
	if err := m.ensureOpen(); err != nil {
		return err
	}
	return m.store.RecordCallTrace(trace)
}

func (m *MemoryDatabase) GetCallTrace(finishedAt time.Time, id int) (*PersistedCallTrace, error) {
	if id <= 0 {
		return nil, errors.New("call trace ID is required")
	}
	if err := m.ensureOpen(); err != nil {
		return nil, err
	}
	return m.store.GetCallTrace(finishedAt, id)
}

func (m *MemoryDatabase) CleanupCallTrace(days int) error {
	if days < 0 {
		return errors.New("cleanup days must not be negative")
	}
	if err := m.ensureOpen(); err != nil {
		return err
	}
	return m.store.CleanupCallTrace(days)
}

func (m *MemoryDatabase) QueryCallTraces(filter CallTraceFilter, page, pageSize int) ([]PersistedCallTraceSummary, int, error) {
	if err := validateTimeRange(filter.TimeRange); err != nil {
		return nil, 0, err
	}
	if err := m.ensureOpen(); err != nil {
		return nil, 0, err
	}
	if page < 1 {
		return nil, 0, errors.New("page must be greater than zero")
	}
	if pageSize < 1 {
		return nil, 0, errors.New("page size must be greater than zero")
	}
	return m.store.QueryCallTraces(filter, page, pageSize)
}

func (m *MemoryDatabase) QueryAccountUsage(timeRange TimeRange) ([]AccountUsageRow, UsageTotals, error) {
	if err := validateTimeRange(timeRange); err != nil {
		return nil, UsageTotals{}, err
	}
	if err := m.ensureOpen(); err != nil {
		return nil, UsageTotals{}, err
	}
	return m.store.QueryAccountUsage(timeRange)
}

func (m *MemoryDatabase) QueryUserUsage(timeRange TimeRange, accountID int) ([]UserUsageRow, UsageTotals, error) {
	if err := validateTimeRange(timeRange); err != nil {
		return nil, UsageTotals{}, err
	}
	if accountID < 0 {
		return nil, UsageTotals{}, errors.New("account ID must not be negative")
	}
	if err := m.ensureOpen(); err != nil {
		return nil, UsageTotals{}, err
	}
	return m.store.QueryUserUsage(timeRange, accountID)
}

// replaceCache refreshes every cached map from the records loaded through the
// store and marks the database open.
func (m *MemoryDatabase) replaceCache(accounts []PersistedAccount, users []PersistedUser, apiKeys []PersistedAPIKey, proxyGroups []PersistedProxyGroup, configs []PersistedConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.opened = true
	m.accounts = make(map[int]PersistedAccount, len(accounts))
	for _, value := range accounts {
		m.accounts[value.ID] = cloneAccount(value)
	}
	m.users = make(map[int]PersistedUser, len(users))
	for _, value := range users {
		m.users[value.ID] = cloneUser(value)
	}
	m.apiKeys = make(map[int]PersistedAPIKey, len(apiKeys))
	for _, value := range apiKeys {
		m.apiKeys[value.ID] = cloneAPIKey(value)
	}
	m.proxyGroups = make(map[int]PersistedProxyGroup, len(proxyGroups))
	for _, value := range proxyGroups {
		m.proxyGroups[value.ID] = cloneProxyGroup(value)
	}
	m.configs = make(map[int]PersistedConfig, len(configs))
	for _, value := range configs {
		m.configs[value.ID] = cloneConfig(value)
	}
}

func (m *MemoryDatabase) cachedCredential(id string) json.RawMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	value, ok := m.credentials[id]
	if !ok {
		return nil
	}
	return slices.Clone(value)
}

func (m *MemoryDatabase) cacheCredential(id string, value json.RawMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.credentials[id] = slices.Clone(value)
}

func (m *MemoryDatabase) dropCredential(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.credentials, id)
}

func (m *MemoryDatabase) cachedAccounts() []PersistedAccount {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]PersistedAccount, 0, len(m.accounts))
	for value := range maps.Values(m.accounts) {
		result = append(result, cloneAccount(value))
	}
	slices.SortFunc(result, func(a, b PersistedAccount) int { return cmp.Compare(a.ID, b.ID) })
	return result
}

func (m *MemoryDatabase) hasAccount(id int) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.accounts[id]
	return ok
}

func (m *MemoryDatabase) cacheAccount(value PersistedAccount) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.accounts[value.ID] = cloneAccount(value)
}

// dropAccount also drops the API keys that belong to the deleted account.
func (m *MemoryDatabase) dropAccount(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.accounts, id)
	maps.DeleteFunc(m.apiKeys, func(_ int, value PersistedAPIKey) bool { return value.AccountID == id })
}

func (m *MemoryDatabase) cachedUsers() []PersistedUser {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]PersistedUser, 0, len(m.users))
	for value := range maps.Values(m.users) {
		result = append(result, cloneUser(value))
	}
	slices.SortFunc(result, func(a, b PersistedUser) int { return cmp.Compare(a.ID, b.ID) })
	return result
}

func (m *MemoryDatabase) hasUser(id int) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.users[id]
	return ok
}

func (m *MemoryDatabase) cacheUser(value PersistedUser) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.users[value.ID] = cloneUser(value)
}

// dropUser also drops the API keys that belong to the deleted user.
func (m *MemoryDatabase) dropUser(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.users, id)
	maps.DeleteFunc(m.apiKeys, func(_ int, value PersistedAPIKey) bool { return value.UserID == id })
}

func (m *MemoryDatabase) cachedAPIKeys(userID int) []PersistedAPIKey {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]PersistedAPIKey, 0)
	for value := range maps.Values(m.apiKeys) {
		if userID == 0 || value.UserID == userID {
			result = append(result, cloneAPIKey(value))
		}
	}
	slices.SortFunc(result, func(a, b PersistedAPIKey) int { return cmp.Compare(a.ID, b.ID) })
	return result
}

func (m *MemoryDatabase) hasAPIKey(id int) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.apiKeys[id]
	return ok
}

func (m *MemoryDatabase) cacheAPIKey(value PersistedAPIKey) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.apiKeys[value.ID] = cloneAPIKey(value)
}

func (m *MemoryDatabase) dropAPIKey(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.apiKeys, id)
}

func (m *MemoryDatabase) cachedProxyGroups() []PersistedProxyGroup {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]PersistedProxyGroup, 0, len(m.proxyGroups))
	for value := range maps.Values(m.proxyGroups) {
		result = append(result, cloneProxyGroup(value))
	}
	slices.SortFunc(result, func(a, b PersistedProxyGroup) int { return cmp.Compare(a.ID, b.ID) })
	return result
}

func (m *MemoryDatabase) cacheProxyGroup(value PersistedProxyGroup) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.proxyGroups[value.ID] = cloneProxyGroup(value)
}

func (m *MemoryDatabase) dropProxyGroup(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.proxyGroups, id)
}

func (m *MemoryDatabase) cachedConfigs() []PersistedConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]PersistedConfig, 0, len(m.configs))
	for value := range maps.Values(m.configs) {
		result = append(result, cloneConfig(value))
	}
	slices.SortFunc(result, func(a, b PersistedConfig) int { return cmp.Compare(a.ID, b.ID) })
	return result
}

func (m *MemoryDatabase) cachedConfigsByType(configType string) []PersistedConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]PersistedConfig, 0)
	for value := range maps.Values(m.configs) {
		if value.Type == configType {
			result = append(result, cloneConfig(value))
		}
	}
	slices.SortFunc(result, func(a, b PersistedConfig) int { return cmp.Compare(a.ID, b.ID) })
	return result
}

func (m *MemoryDatabase) cachedConfigByTypeName(configType, name string) (PersistedConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for value := range maps.Values(m.configs) {
		if value.Type == configType && value.Name == name {
			return cloneConfig(value), true
		}
	}
	return PersistedConfig{}, false
}

func (m *MemoryDatabase) cachedConfig(id int) (PersistedConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	value, ok := m.configs[id]
	if !ok {
		return PersistedConfig{}, false
	}
	return cloneConfig(value), true
}

func (m *MemoryDatabase) cacheConfig(value PersistedConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configs[value.ID] = cloneConfig(value)
}

func (m *MemoryDatabase) dropConfig(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.configs, id)
}

var _ Database = (*MemoryDatabase)(nil)
