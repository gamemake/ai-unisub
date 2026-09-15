package database

import (
	"ai-unisub/internal/proxy"
	"encoding/json"
	"time"
)

func (m *MemoryDatabase) SaveProxyHealth(values []proxy.HealthRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.proxyHealth == nil {
		m.proxyHealth = map[string]proxy.HealthRecord{}
	}
	for _, v := range values {
		m.proxyHealth[v.Address+"\x00"+v.Application] = v
	}
	return nil
}
func (s *SQLiteDatabase) SaveProxyHealth(values []proxy.HealthRecord) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, v := range values {
		raw, err := json.Marshal(v.State)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO proxy_health(address,application,state,updated_at) VALUES(?,?,?,?) ON CONFLICT(address,application) DO UPDATE SET state=excluded.state,updated_at=excluded.updated_at`, v.Address, v.Application, string(raw), v.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
