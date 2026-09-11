package database

import (
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"
)

// MemoryDatabase stores accounts, users, and API keys in memory. Call traces
// are deliberately ignored by this implementation.
type MemoryDatabase struct {
	mu          sync.RWMutex
	opened      bool
	accounts    map[string]PersistedAccount
	users       map[string]PersistedUser
	apiKeys     map[string]PersistedAPIKey
	credentials map[string]json.RawMessage
	proxyGroups map[string]PersistedProxyGroup
}

func validateAccount(value *PersistedAccount) error {
	if value == nil || value.ID == "" {
		return errors.New("account and account ID are required")
	}
	return nil
}

func validateUser(value *PersistedUser) error {
	if value == nil || value.ID == "" {
		return errors.New("user and user ID are required")
	}
	return nil
}

func validateAPIKey(value *PersistedAPIKey) error {
	if value == nil || value.ID == "" {
		return errors.New("API key and key ID are required")
	}
	return nil
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

func (m *MemoryDatabase) ListProxyGroups() ([]PersistedProxyGroup, error) {
	if m == nil {
		return nil, errors.New("memory database is nil")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]PersistedProxyGroup, 0, len(m.proxyGroups))
	for _, v := range m.proxyGroups {
		result = append(result, cloneProxyGroup(v))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
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
	m.credentials[id] = append(json.RawMessage(nil), value...)
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

func (m *MemoryDatabase) ListAccounts() ([]PersistedAccount, error) {
	if m == nil {
		return nil, errors.New("memory database is nil")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]PersistedAccount, 0, len(m.accounts))
	for _, account := range m.accounts {
		result = append(result, cloneAccount(account))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
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
	for key, value := range m.apiKeys {
		if value.AccountID == id {
			delete(m.apiKeys, key)
		}
	}
	return nil
}

func (m *MemoryDatabase) ListUsers() ([]PersistedUser, error) {
	if m == nil {
		return nil, errors.New("memory database is nil")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]PersistedUser, 0, len(m.users))
	for _, user := range m.users {
		result = append(result, cloneUser(user))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
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
	for key, value := range m.apiKeys {
		if value.UserID == id {
			delete(m.apiKeys, key)
		}
	}
	return nil
}

func (m *MemoryDatabase) ListAPIKeys(userID string) ([]PersistedAPIKey, error) {
	if m == nil {
		return nil, errors.New("memory database is nil")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]PersistedAPIKey, 0)
	for _, key := range m.apiKeys {
		if userID == "" || key.UserID == userID {
			result = append(result, key)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
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
func (m *MemoryDatabase) QueryCallTraces(string, string, *int, int, int, *TimeRange, ...string) ([]PersistedCallTraceSummary, int, error) {
	return []PersistedCallTraceSummary{}, 0, nil
}
func (m *MemoryDatabase) GetCallTrace(time.Time, string) (*PersistedCallTrace, error) {
	return nil, ErrCallTraceNotFound
}

func cloneAccount(value PersistedAccount) PersistedAccount {
	value.Config = append([]byte(nil), value.Config...)
	return value
}
func cloneUser(value PersistedUser) PersistedUser {
	value.Labels = append([]string(nil), value.Labels...)
	return value
}
func cloneProxyGroup(value PersistedProxyGroup) PersistedProxyGroup {
	value.Proxies = append([]PersistedProxy(nil), value.Proxies...)
	for i := range value.Proxies {
		value.Proxies[i].ErrorRecords = append([]ProxyErrorRecord(nil), value.Proxies[i].ErrorRecords...)
	}
	return value
}
