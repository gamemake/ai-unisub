package proxy

import "time"

// Historical snapshots never seed live scheduling after a restart.
type HealthRecord struct {
	Address     string    `json:"address"`
	Application string    `json:"application"`
	State       State     `json:"state"`
	UpdatedAt   time.Time `json:"updated_at"`
}
type HealthStore interface{ SaveProxyHealth([]HealthRecord) error }

func (m *Manager) saveHealthLocked() error {
	store, ok := m.store.(HealthStore)
	if !ok {
		return nil
	}
	values := make([]HealthRecord, 0, len(m.states))
	for k, s := range m.states {
		values = append(values, HealthRecord{Address: k.address, Application: k.app, State: *s, UpdatedAt: m.now().UTC()})
	}
	return store.SaveProxyHealth(values)
}
