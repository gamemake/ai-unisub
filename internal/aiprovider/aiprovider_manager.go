package aiprovider

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
)

// AIProviderFactory creates a AIProvider from its instance ID and provider-specific JSON config.
// The manager intentionally does not decode the config because different
// provider types may require different fields. The factory must assign id to
// AIProviderConfig.ID and keep it immutable afterwards.
type AIProviderFactory func(id int, data ProviderData) (AIProvider, error)

// AIProviderManager manages the AIProvider instances used by the system.
type AIProviderManager struct {
	mu            sync.RWMutex
	factories     map[string]AIProviderFactory
	aiProviders   map[int]AIProvider
	accounts      map[int]*Account
	proxyResolver ProxyResolver
	adapters      map[int]string
	catalog       Catalog
	bindings      map[string]affinityBinding
	health        map[int]*memberHealth
	revisions     map[int]uint64
	revision      uint64
	stateStore    func(int, json.RawMessage) error
}

func (m *AIProviderManager) SetProxyResolver(resolver ProxyResolver) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.proxyResolver = resolver
	xs := slices.Collect(maps.Values(m.aiProviders))
	m.mu.Unlock()
	for _, p := range xs {
		if x, ok := p.(interface{ SetProxyResolver(ProxyResolver) }); ok {
			x.SetProxyResolver(resolver)
		}
	}
}

// SetStateStore registers the application persistence callback for provider
// state. aiprovider never imports the database; the callback receives the
// provider-owned JSON payload, including quota.
func (m *AIProviderManager) SetStateStore(store func(int, json.RawMessage) error) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.stateStore = store
	xs := slices.Collect(maps.Values(m.aiProviders))
	m.mu.Unlock()
	for _, p := range xs {
		m.bindStateStore(p, store)
	}
}

func (m *AIProviderManager) bindStateStore(p AIProvider, store func(int, json.RawMessage) error) {
	x, ok := p.(interface{ setStateStore(func(AIProviderState) error) })
	if !ok {
		return
	}
	id := p.Config().ID
	x.setStateStore(func(state AIProviderState) error {
		if store == nil {
			return nil
		}
		raw, err := json.Marshal(state)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrQuotaPersist, err)
		}
		if err := store(id, raw); err != nil {
			return fmt.Errorf("%w: %v", ErrQuotaPersist, err)
		}
		return nil
	})
}

// NewAIProviderManager creates an empty AIProviderManager.
func NewAIProviderManager() *AIProviderManager {
	return &AIProviderManager{
		factories:   make(map[string]AIProviderFactory),
		aiProviders: make(map[int]AIProvider),
		accounts:    make(map[int]*Account),
		adapters:    make(map[int]string),
		catalog:     Catalog{Suppliers: SupplierConfigs()},
		bindings:    make(map[string]affinityBinding),
		health:      make(map[int]*memberHealth),
		revisions:   make(map[int]uint64),
	}
}

// Register adds the factory for a provider type. AIProvider types must be
// unique and cannot be registered more than once.
func (m *AIProviderManager) Register(aiProviderType string, factory AIProviderFactory) error {
	if m == nil {
		return errors.New("provider manager is nil")
	}
	if aiProviderType == "" {
		return errors.New("provider type is empty")
	}
	if factory == nil {
		return errors.New("provider factory is nil")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.factories == nil {
		m.factories = make(map[string]AIProviderFactory)
	}
	if _, exists := m.factories[aiProviderType]; exists {
		return errors.New("provider type is already registered")
	}
	m.factories[aiProviderType] = factory
	return nil
}

// Create creates and stores a AIProvider instance under instanceID.
// The JSON config is passed unchanged to the registered factory.
func (m *AIProviderManager) Create(instanceID int, aiProviderType string, config json.RawMessage, state json.RawMessage) (AIProvider, error) {
	data := ProviderData{Config: config, State: state}

	if m == nil {
		return nil, errors.New("provider manager is nil")
	}
	if instanceID == 0 {
		return nil, errors.New("provider instance ID is empty")
	}

	m.mu.RLock()
	factory, ok := m.factories[aiProviderType]
	m.mu.RUnlock()
	if aiProviderType == "group" {
		factory, ok = func(id int, data ProviderData) (AIProvider, error) {
			return newGroup(id, data)
		}, true
	}
	if !ok {
		return nil, errors.New("provider type is not registered")
	}

	config, err := prepareConfig(instanceID, aiProviderType, data.Config)
	if err != nil {
		return nil, err
	}
	data.Config = config
	aiprovider, err := factory(instanceID, data)
	if err != nil {
		return nil, err
	}
	if aiprovider == nil {
		return nil, errors.New("provider factory returned nil")
	}
	if aiprovider.Config().ID != instanceID {
		return nil, errors.New("provider config ID does not match instance ID")
	}
	if err := aiprovider.RestoreState(data.State); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.aiProviders == nil {
		m.aiProviders = make(map[int]AIProvider)
	}
	if _, exists := m.aiProviders[instanceID]; exists {
		return nil, errors.New("provider instance ID is already in use")
	}
	if err := m.validateRelations(instanceID, aiprovider.Config()); err != nil {
		return nil, err
	}
	m.adapters[instanceID] = aiProviderType
	m.revision++
	m.revisions[instanceID] = m.revision
	m.aiProviders[instanceID] = aiprovider
	if m.accounts == nil {
		m.accounts = make(map[int]*Account)
	}
	m.accounts[instanceID] = &Account{aiprovider: aiprovider}
	if x, ok := aiprovider.(interface{ SetProxyResolver(ProxyResolver) }); ok {
		x.SetProxyResolver(m.proxyResolver)
	}
	if x, ok := aiprovider.(interface{ setQuotaSupplier(func(string) Supplier) }); ok {
		x.setQuotaSupplier(m.quotaSupplier)
	}
	m.bindStateStore(aiprovider, m.stateStore)
	return aiprovider, nil
}

// Export returns the provider-owned persistence payload. Runtime manager
// state, cache locks, request sequencing, and credentials held by managers are
// never included in the result.
func (m *AIProviderManager) Export(id int) (ProviderData, bool) {
	if m == nil {
		return ProviderData{}, false
	}
	m.mu.RLock()
	p, ok := m.aiProviders[id]
	m.mu.RUnlock()
	if !ok || p == nil {
		return ProviderData{}, false
	}
	config, err := json.Marshal(p.Config())
	if err != nil {
		return ProviderData{}, false
	}
	state, err := json.Marshal(p.State())
	if err != nil {
		return ProviderData{}, false
	}
	return ProviderData{Config: config, State: state}, true
}

// Get returns the AIProvider instance stored under instanceID.
func (m *AIProviderManager) Get(instanceID int) (AIProvider, bool) {
	if m == nil {
		return nil, false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	aiprovider, ok := m.aiProviders[instanceID]
	return aiprovider, ok
}

// Remove removes and returns the AIProvider instance stored under instanceID.
func (m *AIProviderManager) Remove(instanceID int) (AIProvider, bool) {
	if m == nil {
		return nil, false
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	aiprovider, ok := m.aiProviders[instanceID]
	if ok {
		if account := m.accounts[instanceID]; account != nil {
			account.mu.Lock()
			account.closed = true
			account.wake()
			account.mu.Unlock()
			delete(m.accounts, instanceID)
		}
		delete(m.aiProviders, instanceID)
		delete(m.adapters, instanceID)
		delete(m.health, instanceID)
		delete(m.revisions, instanceID)
		maps.DeleteFunc(m.bindings, func(_ string, b affinityBinding) bool { return b.id == instanceID })
	}
	return aiprovider, ok
}

func (m *AIProviderManager) GetAccount(id int) (*Account, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.accounts[id]
	return a, ok
}

func (m *AIProviderManager) UpdateConfig(id int, config json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrUnavailable
	}
	config, err := prepareConfig(id, m.adapters[id], config)
	if err != nil {
		return err
	}
	c, err := decodeAIProviderConfig(id, config)
	if err != nil {
		return err
	}
	if err = m.validateRelations(id, c); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.aiprovider.UpdateConfig(config); err != nil {
		return err
	}
	a.wake()
	m.revision++
	m.revisions[id] = m.revision
	delete(m.health, id)
	clear(m.bindings)
	return nil
}
