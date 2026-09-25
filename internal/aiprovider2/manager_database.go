package aiprovider2

import (
	"ai-unisub/internal/database"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
)

const (
	DB_CONFIG_TYPE_SUPPLIER string = "supplier"
)

func (m *providerManager) flashToDB() error {
	m.mu.RLock()
	accounts := make([]*Account, 0, len(m.accounts))
	for _, account := range m.accounts {
		accounts = append(accounts, account)
	}
	m.mu.RUnlock()
	slices.SortFunc(accounts, func(a, b *Account) int { return a.ID - b.ID })
	for _, account := range accounts {
		account.mu.RLock()
		dirty := account.Dirty
		account.mu.RUnlock()
		if dirty {
			if err := m.saveAccount(account); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *providerManager) loadAccounts() error {
	accounts, err := m.db.ListAccounts()
	if err != nil {
		return err
	}

	loaded := make(map[int]*Account, len(accounts))
	for _, a := range accounts {
		account, err := NewAccountFromDatabase(m, a.ID, a.Config, a.State, a.Quota)
		if err != nil {
			return fmt.Errorf("load account %d: %w", a.ID, err)
		}
		account.Dirty = false
		loaded[a.ID] = account
	}
	m.mu.Lock()
	m.accounts = loaded
	m.mu.Unlock()
	return nil
}

func (m *providerManager) saveAccount(a *Account) error {
	if a == nil {
		return errors.New("account is nil")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	config := a.Config
	state := a.State
	quota := cloneQuota(a.Quota)
	configJSON, err := json.Marshal(config)
	if err != nil {
		return err
	}
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return err
	}
	quotaJSON, err := json.Marshal(quota)
	if err != nil {
		return err
	}
	persisted := database.PersistedAccount{
		ID:         a.ID,
		AIProvider: string(config.Kind),
		Name:       config.Name,
		Config:     configJSON,
		State:      stateJSON,
		Quota:      quotaJSON,
	}
	if err := m.db.SaveAccount(&persisted); err != nil {
		return err
	}
	a.ID = persisted.ID
	a.Dirty = false
	return nil
}

func (m *providerManager) loadSuppliers() error {
	suppliers := []Supplier{
		newSupplierAnthropic(m),
		newSupplierOpenAI(m),
		newSupplierXAI(m),
		newSupplierDeepSeek(m),
		newSupplierKimi(m),
		newSupplierZAI(m),
	}

	supplierMap := make(map[string]Supplier, len(suppliers))
	for _, s := range suppliers {
		if _, exists := supplierMap[s.GetID()]; exists {
			return fmt.Errorf("duplicate supplier ID %q", s.GetID())
		}
		supplierMap[s.GetID()] = s
	}
	m.suppliers = suppliers
	m.suppliersMap = supplierMap
	m.suppliersIDs = make(map[string]int)

	configs, err := m.db.ListConfigsByType(DB_CONFIG_TYPE_SUPPLIER)
	if err != nil {
		return err
	}

	for _, d := range configs {
		s, ok := supplierMap[d.Name]
		if !ok {
			if err := m.db.DeleteConfig(d.ID); err != nil {
				return err
			}
			continue
		}

		var overlay SupplierOverlayConfig
		if err := json.Unmarshal(d.Value, &overlay); err != nil {
			return err
		}
		if err := s.SetOverlayConfig(overlay, false); err != nil {
			return fmt.Errorf("load supplier %q overlay config: %w", d.Name, err)
		}
		m.suppliersIDs[d.Name] = d.ID
	}

	return nil
}

func (m *providerManager) saveSupplier(name string, overlay SupplierOverlayConfig) error {
	id, _ := m.suppliersIDs[name]
	value, err := json.Marshal(overlay)
	if err != nil {
		return err
	}
	config := database.PersistedConfig{
		ID:    id,
		Type:  DB_CONFIG_TYPE_SUPPLIER,
		Name:  name,
		Value: value,
	}
	if err := m.db.SaveConfig(&config); err != nil {
		return err
	}
	return nil
}
