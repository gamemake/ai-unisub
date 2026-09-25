package aiprovider2

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

func (m *providerManager) ListSuppliers() []Supplier {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return slices.Clone(m.suppliers)
}

func (m *providerManager) SetOverlayConfig(ctx context.Context, supplierID string, value json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	supplier, err := m.supplier(supplierID)
	if err != nil {
		return err
	}

	var overlay SupplierOverlayConfig
	if err := json.Unmarshal(value, &overlay); err != nil {
		return fmt.Errorf("parse supplier %q overlay config: %w", supplier.GetID(), err)
	}
	config := supplier.GetBuiltinConfig()
	if overlay.Models != nil {
		config.Models = slices.Clone(overlay.Models)
	}
	if overlay.Mappings != nil {
		config.Mappings = slices.Clone(overlay.Mappings)
	}
	if overlay.Weights != nil {
		config.Weights = slices.Clone(overlay.Weights)
	}
	if err := validateSupplierValues(config.Models, config.Mappings, config.Weights); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.storeSupplierOverlay(supplier, overlay)
}

func (m *providerManager) RefreshModels(ctx context.Context, supplierID string, accountID int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	supplier, err := m.supplier(supplierID)
	if err != nil {
		return err
	}
	account, err := m.account(accountID)
	if err != nil {
		return err
	}

	account.mu.RLock()
	accountSupplier := account.Config.Supplier
	accountKind := account.Config.Kind
	account.mu.RUnlock()
	if accountKind == AccountGroup || accountSupplier != supplier.GetID() {
		return fmt.Errorf("account %d does not belong to supplier %q", accountID, supplier.GetID())
	}

	models, err := supplier.RefreshModel(ctx, account)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	overlay := supplier.GetOverlayConfig()
	overlay.Models = slices.Clone(models)
	config := supplier.GetConfig()
	config.Models = slices.Clone(overlay.Models)
	if overlay.Mappings != nil {
		config.Mappings = slices.Clone(overlay.Mappings)
	}
	if err := validateSupplierValues(config.Models, config.Mappings, config.Weights); err != nil {
		return fmt.Errorf("validate refreshed models: %w", err)
	}
	return m.storeSupplierOverlay(supplier, overlay)
}

func (m *providerManager) supplier(id string) (Supplier, error) {
	if m == nil {
		return nil, errManagerNil
	}
	id = strings.ToLower(strings.TrimSpace(id))
	supplier := m.getSupplier(id)
	if supplier == nil {
		return nil, fmt.Errorf("%w: %q", ErrSupplierNotFound, id)
	}
	return supplier, nil
}

func (m *providerManager) storeSupplierOverlay(supplier Supplier, overlay SupplierOverlayConfig) error {
	id := supplier.GetID()

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.saveSupplier(id, overlay); err != nil {
		return fmt.Errorf("save supplier %q overlay config: %w", id, err)
	}
	if m.suppliersIDs[id] == 0 {
		persisted, err := m.db.LoadConfig(DB_CONFIG_TYPE_SUPPLIER, id)
		if err != nil {
			return fmt.Errorf("load supplier %q overlay config: %w", id, err)
		}
		if persisted.ID == 0 {
			return fmt.Errorf("saved supplier %q overlay config has no ID", id)
		}
		m.suppliersIDs[id] = persisted.ID
	}
	if err := supplier.SetOverlayConfig(overlay, false); err != nil {
		return fmt.Errorf("apply supplier %q overlay config: %w", id, err)
	}
	return nil
}
