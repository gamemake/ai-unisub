package proxy

import (
	"context"
	"errors"
	"strings"
)

func (m *Manager) ResolveProxy(ctx context.Context, groupID, app string, tried []string) (*Endpoint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(groupID) == "" {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return nil, ErrClosed
	}
	groups, err := m.store.ListProxyGroups()
	if err != nil {
		return nil, err
	}
	skip := map[string]bool{}
	for _, address := range tried {
		skip[address] = true
	}
	for _, g := range groups {
		if g.ID != groupID {
			continue
		}
		if g.Enabled != nil && !*g.Enabled {
			return nil, ErrUnavailable
		}
		for _, p := range g.Proxies {
			if !p.Enabled {
				continue
			}
			e, err := NewEndpoint(p.URL)
			if err != nil {
				return nil, err
			}
			if skip[e.String()] {
				continue
			}
			network := m.state(e.String(), "")
			application := m.state(e.String(), app)
			if !m.admissible(network) || !m.admissible(application) {
				continue
			}
			if err := m.reserve(e.String(), app, m.now()); err != nil {
				return nil, err
			}
			if network.Status == "unavailable" || network.Status == "half_open" {
				network.Status = "half_open"
				network.InFlight++
				e.lease = newID()
				m.leases[e.lease] = append(m.leases[e.lease], stateKey{e.String(), ""})
			}
			if application != network && (application.Status == "unavailable" || application.Status == "half_open") {
				application.Status = "half_open"
				application.InFlight++
				if e.lease == "" {
					e.lease = newID()
				}
				m.leases[e.lease] = append(m.leases[e.lease], stateKey{e.String(), app})
			}
			return e, nil
		}
		return nil, ErrUnavailable
	}
	return nil, errors.New("proxy group not found")
}
func (m *Manager) admissible(s *State) bool {
	if s.Probing {
		return false
	}
	return s.Status != "unavailable" && s.Status != "half_open" || (!m.now().Before(s.CooldownUntil) && s.InFlight < m.policy.HalfOpenLimit)
}
func (m *Manager) ProxyRetryLimit(groupID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	groups, err := m.store.ListProxyGroups()
	if err != nil {
		return 0
	}
	for _, g := range groups {
		if g.ID == groupID {
			n := 0
			for _, p := range g.Proxies {
				if p.Enabled {
					n++
				}
			}
			if n < 2 {
				return 0
			}
			return min(g.MaxRetries, n-1)
		}
	}
	return 0
}
