package database

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

func validateModuleName(module string) error {
	if module == "" || strings.TrimSpace(module) != module {
		return errors.New("module name is required and must not have surrounding whitespace")
	}
	return nil
}

func (m *MemoryDatabase) LoadModuleConfig(module string) (json.RawMessage, error) {
	if err := validateModuleName(module); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append(json.RawMessage(nil), m.moduleConfigs[module]...), nil
}
func (m *MemoryDatabase) SaveModuleConfig(module string, raw json.RawMessage) error {
	if err := validateModuleName(module); err != nil {
		return err
	}
	if !json.Valid(raw) {
		return errors.New("invalid module configuration JSON")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.moduleConfigs == nil {
		m.moduleConfigs = make(map[string]json.RawMessage)
	}
	m.moduleConfigs[module] = append(json.RawMessage(nil), raw...)
	return nil
}
func (m *MemoryDatabase) DeleteModuleConfig(module string) error {
	if err := validateModuleName(module); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.moduleConfigs, module)
	return nil
}

func (s *SQLiteDatabase) LoadModuleConfig(module string) (json.RawMessage, error) {
	if err := validateModuleName(module); err != nil {
		return nil, err
	}
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	var raw []byte
	err := s.db.QueryRow(`SELECT config FROM module_configs WHERE module=?`, module).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return raw, err
}
func (s *SQLiteDatabase) SaveModuleConfig(module string, raw json.RawMessage) error {
	if err := validateModuleName(module); err != nil {
		return err
	}
	if !json.Valid(raw) {
		return errors.New("invalid module configuration JSON")
	}
	if err := s.ensureOpen(); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO module_configs(module,config) VALUES(?,?) ON CONFLICT(module) DO UPDATE SET config=excluded.config`, module, string(raw))
	return err
}

func (s *SQLiteDatabase) DeleteModuleConfig(module string) error {
	if err := validateModuleName(module); err != nil {
		return err
	}
	if err := s.ensureOpen(); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM module_configs WHERE module=?`, module)
	return err
}
