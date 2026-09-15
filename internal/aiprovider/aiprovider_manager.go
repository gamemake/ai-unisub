package aiprovider

import (
	"encoding/json"
	"errors"
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
}

func (m *AIProviderManager) SetProxyResolver(resolver ProxyResolver) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.proxyResolver = resolver
	xs := make([]AIProvider, 0, len(m.aiProviders))
	for _, p := range m.aiProviders {
		xs = append(xs, p)
	}
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
	if !ok {
		return nil, errors.New("provider type is not registered")
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
	m.aiProviders[instanceID] = aiprovider
	if m.accounts == nil {
		m.accounts = make(map[string]*Account)
	}
	m.accounts[instanceID] = &Account{aiprovider: aiprovider}
	if x, ok := aiprovider.(interface{ SetProxyResolver(ProxyResolver) }); ok {
		x.SetProxyResolver(m.proxyResolver)
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
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrUnavailable
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.aiprovider.UpdateConfig(config); err != nil {
		return err
	}
	a.wake()
	return nil
}
