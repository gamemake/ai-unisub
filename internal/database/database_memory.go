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

// MemoryDatabase stores accounts, users, and API keys in memory. Call traces
// are deliberately ignored by this implementation.
type MemoryDatabase struct {
	moduleConfigs map[string]json.RawMessage
	mu            sync.RWMutex
	opened        bool
	accounts      map[string]PersistedAccount
	users         map[string]PersistedUser
	apiKeys       map[string]PersistedAPIKey
	credentials   map[string]json.RawMessage
	proxyGroups   map[string]PersistedProxyGroup
}

// NewMemoryDatabase creates an empty in-memory database.
func NewMemoryDatabase() *MemoryDatabase {
	return &MemoryDatabase{
		accounts:    make(map[string]PersistedAccount),
		users:       make(map[string]PersistedUser),
		apiKeys:     make(map[string]PersistedAPIKey),
		credentials: make(map[string]json.RawMessage),
		proxyGroups: make(map[string]PersistedProxyGroup),
	}
}

func (m *MemoryDatabase) Open() error {
	if m == nil {
		return errors.New("memory database is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.opened = true
	return nil
}

func (m *MemoryDatabase) Close() error {
	if m == nil {
		return errors.New("memory database is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.opened = false
	return nil
}

func (m *MemoryDatabase) LoadModuleConfig(module string) (json.RawMessage, error) {
	if err := validateModuleName(module); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append(json.RawMessage(nil), m.moduleConfigs[module]...), nil
}

func (m *MemoryDatabase) SaveModuleConfig(module string, raw json.RawMessage) error {
	if err := validateModuleName(module); err != nil {
		return err
	}
	if !json.Valid(raw) {
		return errors.New("invalid module configuration JSON")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.moduleConfigs == nil {
		m.moduleConfigs = make(map[string]json.RawMessage)
	}
	m.moduleConfigs[module] = slices.Clone(raw)
	return nil
}

func (m *MemoryDatabase) DeleteModuleConfig(module string) error {
	if err := validateModuleName(module); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.moduleConfigs, module)
	return nil
}

func (m *MemoryDatabase) ListProxyGroups() ([]PersistedProxyGroup, error) {
	if m == nil {
		return nil, errors.New("memory database is nil")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]PersistedProxyGroup, 0, len(m.proxyGroups))
	for v := range maps.Values(m.proxyGroups) {
		result = append(result, cloneProxyGroup(v))
	}
	slices.SortFunc(result, func(a, b PersistedProxyGroup) int { return cmp.Compare(a.ID, b.ID) })
	return result, nil
}
func (m *MemoryDatabase) SaveProxyGroup(value *PersistedProxyGroup) error {
	if m == nil {
		return errors.New("memory database is nil")
	}
	if value == nil || value.ID == "" {
		return errors.New("proxy group and ID are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.proxyGroups == nil {
		m.proxyGroups = make(map[string]PersistedProxyGroup)
	}
	m.proxyGroups[value.ID] = cloneProxyGroup(*value)
	return nil
}
func (m *MemoryDatabase) DeleteProxyGroup(id string) error {
	if m == nil {
		return errors.New("memory database is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.proxyGroups, id)
	return nil
}

func (m *MemoryDatabase) RecordProxyLog(value *PersistedProxyLog) error {
	if m == nil {
		return errors.New("memory database is nil")
	}
	return errors.New("proxy logs are not supported by memory database")
}

func (m *MemoryDatabase) QueryProxyLogs(filter ProxyLogFilter, page, pageSize int) ([]PersistedProxyLog, int, error) {
	if m == nil {
		return nil, 0, errors.New("memory database is nil")
	}
	return nil, 0, errors.New("proxy logs are not supported by memory database")
}

func (m *MemoryDatabase) CleanupProxyLog(days int) error {
	if m == nil {
		return errors.New("memory database is nil")
	}
	return errors.New("proxy logs are not supported by memory database")
}

func (m *MemoryDatabase) LoadCredential(id string) (json.RawMessage, error) {
	if m == nil {
		return nil, errors.New("memory database is nil")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	value, ok := m.credentials[id]
	if !ok {
		return nil, errors.New("credential not found")
	}
	return append(json.RawMessage(nil), value...), nil
}

func (m *MemoryDatabase) SaveCredential(id string, value json.RawMessage) error {
	if m == nil {
		return errors.New("memory database is nil")
	}
	if id == "" || len(value) == 0 || !json.Valid(value) {
		return errors.New("credential ID and credential are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.credentials == nil {
		m.credentials = make(map[string]json.RawMessage)
	}
	m.credentials[id] = slices.Clone(value)
	return nil
}

func (m *MemoryDatabase) DeleteCredential(id string) error {
	if m == nil {
		return errors.New("memory database is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.credentials, id)
	return nil
}

func (m *MemoryDatabase) ListAccounts() ([]PersistedAccount, error) {
	if m == nil {
		return nil, errors.New("memory database is nil")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]PersistedAccount, 0, len(m.accounts))
	for account := range maps.Values(m.accounts) {
		result = append(result, cloneAccount(account))
	}
	slices.SortFunc(result, func(a, b PersistedAccount) int { return cmp.Compare(a.ID, b.ID) })
	return result, nil
}

func (m *MemoryDatabase) SaveAccount(account *PersistedAccount) error {
	if m == nil {
		return errors.New("memory database is nil")
	}
	if err := validateAccount(account); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.accounts == nil {
		m.accounts = make(map[string]PersistedAccount)
	}
	m.accounts[account.ID] = cloneAccount(*account)
	return nil
}

func (m *MemoryDatabase) DeleteAccount(id string) error {
	if m == nil {
		return errors.New("memory database is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.accounts, id)
	maps.DeleteFunc(m.apiKeys, func(_ string, value PersistedAPIKey) bool { return value.AccountID == id })
	return nil
}

func (m *MemoryDatabase) ListUsers() ([]PersistedUser, error) {
	if m == nil {
		return nil, errors.New("memory database is nil")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]PersistedUser, 0, len(m.users))
	for user := range maps.Values(m.users) {
		result = append(result, cloneUser(user))
	}
	slices.SortFunc(result, func(a, b PersistedUser) int { return cmp.Compare(a.ID, b.ID) })
	return result, nil
}

func (m *MemoryDatabase) SaveUser(user *PersistedUser) error {
	if m == nil {
		return errors.New("memory database is nil")
	}
	if err := validateUser(user); err != nil {
		return err
	}
	// Preserve the zero-value behavior of users created by older callers.
	if !user.Enabled && user.UpdatedAt.IsZero() {
		user.Enabled = true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.users == nil {
		m.users = make(map[string]PersistedUser)
	}
	m.users[user.ID] = cloneUser(*user)
	return nil
}

func (m *MemoryDatabase) DeleteUser(id string) error {
	if m == nil {
		return errors.New("memory database is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.users, id)
	maps.DeleteFunc(m.apiKeys, func(_ string, value PersistedAPIKey) bool { return value.UserID == id })
	return nil
}

func (m *MemoryDatabase) ListAPIKeys(userID string) ([]PersistedAPIKey, error) {
	if m == nil {
		return nil, errors.New("memory database is nil")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]PersistedAPIKey, 0)
	for key := range maps.Values(m.apiKeys) {
		if userID == "" || key.UserID == userID {
			result = append(result, key)
		}
	}
	slices.SortFunc(result, func(a, b PersistedAPIKey) int { return cmp.Compare(a.ID, b.ID) })
	return result, nil
}

func (m *MemoryDatabase) SaveAPIKey(key *PersistedAPIKey) error {
	if m == nil {
		return errors.New("memory database is nil")
	}
	if err := validateAPIKey(key); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.apiKeys == nil {
		m.apiKeys = make(map[string]PersistedAPIKey)
	}
	m.apiKeys[key.ID] = *key
	return nil
}

func (m *MemoryDatabase) DeleteAPIKey(id string) error {
	if m == nil {
		return errors.New("memory database is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.apiKeys, id)
	return nil
}

func (m *MemoryDatabase) RecordCallTrace(*PersistedCallTrace) error { return nil }
func (m *MemoryDatabase) CleanupCallTrace(int) error                { return nil }
func (m *MemoryDatabase) QueryCallTraces(filter CallTraceFilter, page, pageSize int) ([]PersistedCallTraceSummary, int, error) {
	return []PersistedCallTraceSummary{}, 0, nil
}
func (m *MemoryDatabase) GetCallTrace(time.Time, string) (*PersistedCallTrace, error) {
	return nil, ErrCallTraceNotFound
}
