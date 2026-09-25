package aiprovider2

import (
	"ai-unisub/internal/database"
	"ai-unisub/internal/proxy"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

const flashInterval = 30 * time.Second

type ProviderManager interface {
	Open() error
	Close() error

	ListAccounts() []*Account
	NewAccount(value json.RawMessage) (*Account, error)
	DelAccount(id int) error
	SetAccountConfig(ctx context.Context, id int, value json.RawMessage) error
	FetchQuota(ctx context.Context, id int) (AccountQuota, error)
	ResetQuota(ctx context.Context, id int, resetType string) error
	GetModels(ctx context.Context, id int, clientType string) ([]string, error)

	ListSuppliers() []Supplier
	SetOverlayConfig(ctx context.Context, supplierID string, value json.RawMessage) error
	RefreshModels(ctx context.Context, supplierID string, accountID int) error
}

type providerManager struct {
	db    database.Database
	proxy *proxy.Manager

	mu           sync.RWMutex
	accounts     map[int]*Account
	suppliers    []Supplier
	suppliersMap map[string]Supplier
	suppliersIDs map[string]int

	lifecycleMu sync.Mutex
	stopFlash   chan struct{}
	flashDone   chan struct{}
}

var _ ProviderManager = (*providerManager)(nil)

func NewProviderManager(db database.Database, proxy *proxy.Manager) (ProviderManager, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	if proxy == nil {
		return nil, errors.New("proxy manager is required")
	}
	manager := &providerManager{
		db:           db,
		proxy:        proxy,
		accounts:     make(map[int]*Account),
		suppliersMap: make(map[string]Supplier),
		suppliersIDs: make(map[string]int),
	}
	return manager, nil
}

func (m *providerManager) Open() error {
	if err := m.loadSuppliers(); err != nil {
		return err
	}
	if err := m.loadAccounts(); err != nil {
		return err
	}

	m.lifecycleMu.Lock()
	if m.stopFlash == nil {
		m.stopFlash = make(chan struct{})
		m.flashDone = make(chan struct{})

		go func(stop <-chan struct{}, done chan<- struct{}) {
			defer close(done)

			ticker := time.NewTicker(flashInterval)
			defer ticker.Stop()

			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					_ = m.flashToDB()
				}
			}
		}(m.stopFlash, m.flashDone)
	}
	m.lifecycleMu.Unlock()
	return nil
}

func (m *providerManager) Close() error {
	m.lifecycleMu.Lock()
	stopFlash := m.stopFlash
	flashDone := m.flashDone
	m.stopFlash = nil
	m.flashDone = nil
	if stopFlash != nil {
		close(stopFlash)
	}
	m.lifecycleMu.Unlock()

	if flashDone != nil {
		<-flashDone
	}
	return m.flashToDB()
}

func (m *providerManager) getAccount(id int) *Account {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.accounts[id]
}

func (m *providerManager) getSupplier(name string) Supplier {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.suppliersMap[name]
}
