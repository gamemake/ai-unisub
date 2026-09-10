package provider

import (
	"encoding/json"
	"errors"
	"sync"
)

// ProviderFactory creates a Provider from its instance ID and provider-specific JSON config.
// The manager intentionally does not decode the config because different
// provider types may require different fields. The factory must assign id to
// ProviderConfig.ID and keep it immutable afterwards.
type ProviderFactory func(id string, config json.RawMessage) (Provider, error)

// ProviderManager manages the Provider instances used by the system.
type ProviderManager struct {
	mu        sync.RWMutex
	factories map[string]ProviderFactory
	providers map[string]Provider
}

// NewProviderManager creates an empty ProviderManager.
func NewProviderManager() *ProviderManager {
	return &ProviderManager{
		factories: make(map[string]ProviderFactory),
		providers: make(map[string]Provider),
	}
}

// Register adds the factory for a provider type. Provider types must be
// unique and cannot be registered more than once.
func (m *ProviderManager) Register(providerType string, factory ProviderFactory) error {
	if m == nil {
		return errors.New("provider manager is nil")
	}
	if providerType == "" {
		return errors.New("provider type is empty")
	}
	if factory == nil {
		return errors.New("provider factory is nil")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.factories == nil {
		m.factories = make(map[string]ProviderFactory)
	}
	if _, exists := m.factories[providerType]; exists {
		return errors.New("provider type is already registered")
	}
	m.factories[providerType] = factory
	return nil
}

// Create creates and stores a Provider instance under instanceID.
// The JSON config is passed unchanged to the registered factory.
func (m *ProviderManager) Create(instanceID, providerType string, config json.RawMessage) (Provider, error) {
	if m == nil {
		return nil, errors.New("provider manager is nil")
	}
	if instanceID == "" {
		return nil, errors.New("provider instance ID is empty")
	}

	m.mu.RLock()
	factory, ok := m.factories[providerType]
	m.mu.RUnlock()
	if !ok {
		return nil, errors.New("provider type is not registered")
	}

	provider, err := factory(instanceID, config)
	if err != nil {
		return nil, err
	}
	if provider == nil {
		return nil, errors.New("provider factory returned nil")
	}
	if provider.Config().ID != instanceID {
		return nil, errors.New("provider config ID does not match instance ID")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.providers == nil {
		m.providers = make(map[string]Provider)
	}
	if _, exists := m.providers[instanceID]; exists {
		return nil, errors.New("provider instance ID is already in use")
	}
	m.providers[instanceID] = provider
	return provider, nil
}

// Get returns the Provider instance stored under instanceID.
func (m *ProviderManager) Get(instanceID string) (Provider, bool) {
	if m == nil {
		return nil, false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	provider, ok := m.providers[instanceID]
	return provider, ok
}

// Remove removes and returns the Provider instance stored under instanceID.
func (m *ProviderManager) Remove(instanceID string) (Provider, bool) {
	if m == nil {
		return nil, false
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	provider, ok := m.providers[instanceID]
	if ok {
		delete(m.providers, instanceID)
	}
	return provider, ok
}
