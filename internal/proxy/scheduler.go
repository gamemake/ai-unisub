package proxy

import (
	"context"
	"errors"
	"fmt"
)

func (m *Manager) ResolveProxy(ctx context.Context, groupID int, app string, tried []string) (endpoint *Endpoint, err error) {
	reason, address := "context", ""
	var disabled, triedCount, networkBlocked, applicationBlocked int
	defer func() {
		if err != nil {
			logOperationError("resolve", address, groupID, app, err, fmt.Sprintf("reason=%s disabled=%d tried=%d network_blocked=%d application_blocked=%d", reason, disabled, triedCount, networkBlocked, applicationBlocked))
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if groupID == 0 {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		reason = "manager_closed"
		return nil, ErrClosed
	}
	reason = "list_groups"
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
			reason = "group_disabled"
			return nil, ErrUnavailable
		}
		for _, p := range g.Proxies {
			if !p.Enabled {
				disabled++
				continue
			}
			address, reason = p.URL, "invalid_endpoint"
			e, err := newEndpoint(p.URL)
			if err != nil {
				return nil, err
			}
			if skip[e.String()] {
				triedCount++
				continue
			}
			network := m.state(e.String(), "")
			application := m.state(e.String(), app)
			if !m.admissible(network) {
				networkBlocked++
				continue
			}
			if !m.admissible(application) {
				applicationBlocked++
				continue
			}
			reason = "reserve_statistics"
			if err := m.reserve(e.String(), app, m.now()); err != nil {
				return nil, err
			}
			if network.Status == "unavailable" || network.Status == "half_open" {
				before := *network
				network.Status = "half_open"
				network.InFlight++
				e.lease = newID()
				m.leases[e.lease] = append(m.leases[e.lease], stateKey{e.String(), ""})
				logState(stateKey{e.String(), ""}, before, network, "scheduler", "cooldown_elapsed")
			}
			if application != network && (application.Status == "unavailable" || application.Status == "half_open") {
				before := *application
				application.Status = "half_open"
				application.InFlight++
				if e.lease == "" {
					e.lease = newID()
				}
				m.leases[e.lease] = append(m.leases[e.lease], stateKey{e.String(), app})
				logState(stateKey{e.String(), app}, before, application, "scheduler", "cooldown_elapsed")
			}
			return e, nil
		}
		address, reason = "", "no_admissible_candidate"
		return nil, ErrUnavailable
	}
	reason = "group_not_found"
	return nil, errors.New("proxy group not found")
}
func (m *Manager) admissible(s *State) bool {
	if s.Probing {
		return false
	}
	return s.Status != "unavailable" && s.Status != "half_open" || (!m.now().Before(s.CooldownUntil) && s.InFlight < m.policy.HalfOpenLimit)
}
func (m *Manager) ProxyRetryLimit(groupID int) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	groups, err := m.store.ListProxyGroups()
	if err != nil {
		logOperationError("retry_limit", "", groupID, "", err)
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
