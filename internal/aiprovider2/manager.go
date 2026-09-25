package aiprovider2

import (
	"ai-unisub/internal/database"
	"ai-unisub/internal/oauth2"
	"ai-unisub/internal/proxy2"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

const flashInterval = 30 * time.Second

type ProviderManager interface {
	Open() error
	Close() error
	Handler() http.Handler

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
	db      database.Database
	proxy   proxy2.ProxyManager
	oauth   oauth2.OAuthManager
	gateway Gateway

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

func NewProviderManager(db database.Database, proxyManager proxy2.ProxyManager, oauthManager oauth2.OAuthManager) (ProviderManager, error) {
	if db == nil {
		return nil, errDatabaseRequired
	}
	if proxyManager == nil {
		return nil, errProxyManagerRequired
	}
	manager := &providerManager{
		db:           db,
		proxy:        proxyManager,
		oauth:        oauthManager,
		accounts:     make(map[int]*Account),
		suppliersMap: make(map[string]Supplier),
		suppliersIDs: make(map[string]int),
	}
	gateway, err := NewGateway(manager)
	if err != nil {
		return nil, err
	}
	manager.gateway = gateway
	return manager, nil
}

func (m *providerManager) Handler() http.Handler {
	return http.HandlerFunc(m.gateway.Handle)
}

func (m *providerManager) OnAuthAndAccount(apiKey string, req *http.Request) (int, *Account, GatewaySupplierListener, error) {
	if m == nil || m.db == nil {
		return 0, nil, nil, errProviderManagerNotConfigured
	}
	keys, err := m.db.ListAPIKeys(0)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("list API keys: %w", err)
	}
	var matched database.PersistedAPIKey
	now := time.Now().UTC()
	for _, candidate := range keys {
		if subtle.ConstantTimeCompare([]byte(candidate.Key), []byte(apiKey)) != 1 {
			continue
		}
		if candidate.ValidSeconds > 0 && !candidate.CreatedAt.Add(time.Duration(candidate.ValidSeconds)*time.Second).After(now) {
			continue
		}
		matched = candidate
		break
	}
	if matched.ID <= 0 {
		return 0, nil, nil, ErrGatewayUnauthorized
	}

	users, err := m.db.ListUsers()
	if err != nil {
		return 0, nil, nil, fmt.Errorf("list users: %w", err)
	}
	userEnabled := false
	for _, user := range users {
		if user.ID == matched.UserID {
			userEnabled = user.Enabled
			break
		}
	}
	if !userEnabled {
		return 0, nil, nil, ErrGatewayForbidden
	}

	root := m.getAccount(matched.AccountID)
	if root == nil {
		return 0, nil, nil, ErrGatewayForbidden
	}
	root.mu.RLock()
	rootEnabled := root.Config.Enabled
	root.mu.RUnlock()
	if !rootEnabled {
		return 0, nil, nil, ErrGatewayForbidden
	}
	account, err := root.accountForRequest(req)
	if err != nil {
		return 0, nil, nil, err
	}
	account.mu.RLock()
	enabled := account.Config.Enabled
	supplierID := account.Config.Supplier
	account.mu.RUnlock()
	if !enabled {
		return 0, nil, nil, ErrUnavailable
	}
	supplier := m.getSupplier(supplierID)
	if supplier == nil {
		return 0, nil, nil, fmt.Errorf("%w: %q", ErrSupplierNotFound, supplierID)
	}
	return matched.UserID, account, supplier, nil
}

func (m *providerManager) RecordCallTrace(trace *database.PersistedCallTrace) error {
	if m == nil || m.db == nil {
		return errProviderManagerDatabaseNotConfigured
	}
	return m.db.RecordCallTrace(trace)
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
