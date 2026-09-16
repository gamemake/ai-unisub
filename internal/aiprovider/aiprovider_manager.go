package aiprovider

import (
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"sync"
)

// AIProviderFactory creates a AIProvider from its instance ID and provider-specific JSON config.
// The manager intentionally does not decode the config because different
// provider types may require different fields. The factory must assign id to
// AIProviderConfig.ID and keep it immutable afterwards.
type AIProviderFactory func(id string, config json.RawMessage) (AIProvider, error)

// AIProviderManager manages the AIProvider instances used by the system.
type AIProviderManager struct {
	mu            sync.RWMutex
	factories     map[string]AIProviderFactory
	aiProviders   map[string]AIProvider
	accounts      map[string]*Account
	proxyResolver ProxyResolver
	adapters      map[string]string
	catalog       Catalog
	bindings      map[string]affinityBinding
	health        map[string]*memberHealth
	revisions     map[string]uint64
	revision      uint64
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

// NewAIProviderManager creates an empty AIProviderManager.
func NewAIProviderManager() *AIProviderManager {
	return &AIProviderManager{
		factories:   make(map[string]AIProviderFactory),
		aiProviders: make(map[string]AIProvider),
		accounts:    make(map[string]*Account),
		adapters:    make(map[string]string),
		catalog:     Catalog{Suppliers: SupplierConfigs()},
		bindings:    make(map[string]affinityBinding),
		health:      make(map[string]*memberHealth),
		revisions:   make(map[string]uint64),
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
func (m *AIProviderManager) Create(instanceID, aiProviderType string, config json.RawMessage) (AIProvider, error) {
	if m == nil {
		return nil, errors.New("provider manager is nil")
	}
	if instanceID == "" {
		return nil, errors.New("provider instance ID is empty")
	}

	m.mu.RLock()
	factory, ok := m.factories[aiProviderType]
	m.mu.RUnlock()
	if aiProviderType == "group" {
		factory, ok = newGroup, true
	}
	if !ok {
		return nil, errors.New("provider type is not registered")
	}

	config, err := prepareConfig(instanceID, aiProviderType, config)
	if err != nil {
		return nil, err
	}
	aiprovider, err := factory(instanceID, config)
	if err != nil {
		return nil, err
	}
	if aiprovider == nil {
		return nil, errors.New("provider factory returned nil")
	}
	if aiprovider.Config().ID != instanceID {
		return nil, errors.New("provider config ID does not match instance ID")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.aiProviders == nil {
		m.aiProviders = make(map[string]AIProvider)
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
		m.accounts = make(map[string]*Account)
	}
	m.accounts[instanceID] = &Account{aiprovider: aiprovider}
	if x, ok := aiprovider.(interface{ SetProxyResolver(ProxyResolver) }); ok {
		x.SetProxyResolver(m.proxyResolver)
	}
	if x, ok := aiprovider.(interface{ setQuotaSupplier(func(string) Supplier) }); ok {
		x.setQuotaSupplier(m.quotaSupplier)
	}
	return aiprovider, nil
}

// Get returns the AIProvider instance stored under instanceID.
func (m *AIProviderManager) Get(instanceID string) (AIProvider, bool) {
	if m == nil {
		return nil, false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	aiprovider, ok := m.aiProviders[instanceID]
	return aiprovider, ok
}

// Remove removes and returns the AIProvider instance stored under instanceID.
func (m *AIProviderManager) Remove(instanceID string) (AIProvider, bool) {
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

func (m *AIProviderManager) GetAccount(id string) (*Account, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.accounts[id]
	return a, ok
}

func (m *AIProviderManager) UpdateConfig(id string, config json.RawMessage) error {
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
