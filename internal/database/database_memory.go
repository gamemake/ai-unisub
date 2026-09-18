package database

import (
	"cmp"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"sync"
)

// MemoryDatabase is the process-local cache used by SQLiteDatabase. It is not
// a Database implementation and has no persistence or lifecycle semantics.
type MemoryDatabase struct {
	mu            sync.RWMutex
	moduleConfigs map[string]json.RawMessage
	credentials   map[string]json.RawMessage
	accounts      map[int]PersistedAccount
	users         map[int]PersistedUser
	apiKeys       map[int]PersistedAPIKey
	proxyGroups   map[int]PersistedProxyGroup
}

func NewMemoryDatabase() *MemoryDatabase {
	return &MemoryDatabase{
		moduleConfigs: make(map[string]json.RawMessage), credentials: make(map[string]json.RawMessage),
		accounts: make(map[int]PersistedAccount), users: make(map[int]PersistedUser),
		apiKeys: make(map[int]PersistedAPIKey), proxyGroups: make(map[int]PersistedProxyGroup),
	}
}

func (m *MemoryDatabase) LoadModuleConfig(name string) json.RawMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return slices.Clone(m.moduleConfigs[name])
}
func (m *MemoryDatabase) SaveModuleConfig(name string, value json.RawMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.moduleConfigs[name] = slices.Clone(value)
}

func (m *MemoryDatabase) GetCredential(id string) json.RawMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	value, ok := m.credentials[id]
	if !ok {
		return nil
	}
	return slices.Clone(value)
}

func (m *MemoryDatabase) SetCredential(id string, value json.RawMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.credentials[id] = slices.Clone(value)
}

func (m *MemoryDatabase) DeleteCredential(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.credentials, id)
}

func (m *MemoryDatabase) ListAccounts() []PersistedAccount {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]PersistedAccount, 0, len(m.accounts))
	for value := range maps.Values(m.accounts) {
		result = append(result, cloneAccount(value))
	}
	slices.SortFunc(result, func(a, b PersistedAccount) int { return cmp.Compare(a.ID, b.ID) })
	return result
}

func (m *MemoryDatabase) HasAccount(id int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.accounts[id]
	return ok
}

func (m *MemoryDatabase) SaveAccount(value PersistedAccount) error {
	if value.ID <= 0 {
		return errors.New("account ID must be positive")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.accounts[value.ID] = cloneAccount(value)
	return nil
}

func (m *MemoryDatabase) DeleteAccount(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.accounts, id)
	maps.DeleteFunc(m.apiKeys, func(_ int, value PersistedAPIKey) bool { return value.AccountID == id })
}

func (m *MemoryDatabase) ListUsers() []PersistedUser {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]PersistedUser, 0, len(m.users))
	for value := range maps.Values(m.users) {
		result = append(result, cloneUser(value))
	}
	slices.SortFunc(result, func(a, b PersistedUser) int { return cmp.Compare(a.ID, b.ID) })
	return result
}

func (m *MemoryDatabase) HasUser(id int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.accounts[id]
	return ok
}

func (m *MemoryDatabase) SaveUser(value PersistedUser) error {
	if value.ID <= 0 {
		return errors.New("user ID must be positive")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.users[value.ID] = cloneUser(value)
	return nil
}

func (m *MemoryDatabase) DeleteUser(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.users, id)
	maps.DeleteFunc(m.apiKeys, func(_ int, value PersistedAPIKey) bool { return value.UserID == id })
}

func (m *MemoryDatabase) ListAPIKeys(userID int) []PersistedAPIKey {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]PersistedAPIKey, 0)
	for value := range maps.Values(m.apiKeys) {
		if userID == 0 || value.UserID == userID {
			result = append(result, value)
		}
	}
	slices.SortFunc(result, func(a, b PersistedAPIKey) int { return cmp.Compare(a.ID, b.ID) })
	return result
}

func (m *MemoryDatabase) HasAPIKey(id int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.apiKeys[id]
	return ok
}

func (m *MemoryDatabase) SaveAPIKey(value PersistedAPIKey) error {
	if value.ID <= 0 {
		return errors.New("API key ID must be positive")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.apiKeys[value.ID] = value
	return nil
}
func (m *MemoryDatabase) DeleteAPIKey(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.apiKeys, id)
}

func (m *MemoryDatabase) ListProxyGroups() []PersistedProxyGroup {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]PersistedProxyGroup, 0, len(m.proxyGroups))
	for value := range maps.Values(m.proxyGroups) {
		result = append(result, cloneProxyGroup(value))
	}
	slices.SortFunc(result, func(a, b PersistedProxyGroup) int { return cmp.Compare(a.ID, b.ID) })
	return result
}
func (m *MemoryDatabase) SaveProxyGroup(value PersistedProxyGroup) error {
	if value.ID <= 0 {
		return errors.New("proxy group ID must be positive")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.proxyGroups[value.ID] = cloneProxyGroup(value)
	return nil
}
func (m *MemoryDatabase) DeleteProxyGroup(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.proxyGroups, id)
}
