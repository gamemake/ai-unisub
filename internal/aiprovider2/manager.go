package aiprovider2

import (
	"ai-unisub/internal/database"
	"ai-unisub/internal/proxy"
	"errors"
	"sync"
	"time"
)

const flashInterval = 30 * time.Second

type Manager struct {
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

func NewManager(db database.Database, proxy *proxy.Manager) (*Manager, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	if proxy == nil {
		return nil, errors.New("proxy manager is required")
	}
	manager := &Manager{
		db:           db,
		proxy:        proxy,
		accounts:     make(map[int]*Account),
		suppliersMap: make(map[string]Supplier),
		suppliersIDs: make(map[string]int),
	}
	return manager, nil
}

func (m *Manager) Open() error {
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

func (m *Manager) Close() error {
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

func (m *Manager) getAccount(id int) *Account {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.accounts[id]
}

func (m *Manager) getSupplier(name string) Supplier {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.suppliersMap[name]
}
