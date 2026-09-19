package service

import (
	"ai-unisub/internal/aiprovider"
	"ai-unisub/internal/database"
	"encoding/json"
)

// supplierOverlayStore adapts database.PersistedConfig rows for aiprovider.
type supplierOverlayStore struct {
	db database.Database
}

func newSupplierOverlayStore(db database.Database) *supplierOverlayStore {
	return &supplierOverlayStore{db: db}
}

func (s *supplierOverlayStore) ListSupplierOverlays() (map[string]json.RawMessage, error) {
	rows, err := s.db.ListConfigsByType(aiprovider.SupplierConfigType)
	if err != nil {
		return nil, err
	}
	out := make(map[string]json.RawMessage, len(rows))
	for _, row := range rows {
		out[row.Name] = append(json.RawMessage(nil), row.Value...)
	}
	return out, nil
}

func (s *supplierOverlayStore) PutSupplierOverlay(name string, value json.RawMessage) error {
	existing, err := s.db.LoadConfig(aiprovider.SupplierConfigType, name)
	if err != nil {
		return err
	}
	cfg := database.PersistedConfig{
		ID:    existing.ID,
		Type:  aiprovider.SupplierConfigType,
		Name:  name,
		Value: value,
	}
	return s.db.SaveConfig(&cfg)
}

func (s *supplierOverlayStore) DeleteSupplierOverlay(name string) error {
	existing, err := s.db.LoadConfig(aiprovider.SupplierConfigType, name)
	if err != nil {
		return err
	}
	if existing.ID == 0 {
		return nil
	}
	return s.db.DeleteConfig(existing.ID)
}
