package aiprovider2

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
)

func (m *providerManager) ListAccounts() []*Account {
	if m == nil {
		return nil
	}

	m.mu.RLock()
	accounts := make([]*Account, 0, len(m.accounts))
	for _, account := range m.accounts {
		accounts = append(accounts, account)
	}
	m.mu.RUnlock()

	slices.SortFunc(accounts, func(a, b *Account) int {
		return cmp.Compare(a.ID, b.ID)
	})
	return accounts
}

func (m *providerManager) NewAccount(value json.RawMessage) (*Account, error) {
	if m == nil {
		return nil, errManagerNil
	}

	account, err := NewAccount(m, 0, value)
	if err != nil {
		return nil, err
	}
	if err := m.saveAccount(account); err != nil {
		return nil, fmt.Errorf("save account: %w", err)
	}

	m.mu.Lock()
	if _, exists := m.accounts[account.ID]; exists {
		m.mu.Unlock()
		_ = m.db.DeleteAccount(account.ID)
		return nil, fmt.Errorf("account %d already exists", account.ID)
	}
	m.accounts[account.ID] = account
	m.mu.Unlock()
	return account, nil
}

func (m *providerManager) DelAccount(id int) error {
	if m == nil {
		return errManagerNil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	account, exists := m.accounts[id]
	if !exists {
		return fmt.Errorf("%w: %d", ErrAccountNotFound, id)
	}
	if err := m.db.DeleteAccount(id); err != nil {
		return fmt.Errorf("delete account %d: %w", id, err)
	}
	account.close()
	delete(m.accounts, id)
	return nil
}

func (m *providerManager) SetAccountConfig(ctx context.Context, id int, value json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	account, err := m.account(id)
	if err != nil {
		return err
	}
	if err := account.UpdateConfig(value); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.saveAccount(account); err != nil {
		return fmt.Errorf("save account %d: %w", id, err)
	}
	return nil
}

func (m *providerManager) FetchQuota(ctx context.Context, id int) (AccountQuota, error) {
	if err := ctx.Err(); err != nil {
		return AccountQuota{}, err
	}
	account, err := m.account(id)
	if err != nil {
		return AccountQuota{}, err
	}
	quota, err := account.FetchQuota(ctx)
	if err != nil {
		return AccountQuota{}, err
	}
	if err := m.saveAccount(account); err != nil {
		return AccountQuota{}, fmt.Errorf("save account %d quota: %w", id, err)
	}
	return quota, nil
}

func (m *providerManager) ResetQuota(ctx context.Context, id int, resetType string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	account, err := m.account(id)
	if err != nil {
		return err
	}
	if err := account.ResetQuota(ctx, resetType); err != nil {
		return err
	}
	if err := m.saveAccount(account); err != nil {
		return fmt.Errorf("save account %d quota: %w", id, err)
	}
	return nil
}

func (m *providerManager) GetModels(ctx context.Context, id int, _ string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	account, err := m.account(id)
	if err != nil {
		return nil, err
	}
	return account.GetModels(), nil
}

func (m *providerManager) account(id int) (*Account, error) {
	if m == nil {
		return nil, errManagerNil
	}
	account := m.getAccount(id)
	if account == nil {
		return nil, fmt.Errorf("%w: %d", ErrAccountNotFound, id)
	}
	return account, nil
}
