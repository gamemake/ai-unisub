package proxy

import (
	"context"
	"errors"
	"net/http"
	"time"
)

func probe(ctx context.Context, e *Endpoint) error {
	client := Client(&http.Client{}, e)
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, "GET", "https://www.gstatic.com/generate_204", nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if resp != nil {
		resp.Body.Close()
	}
	return err
}
func (m *Manager) SetProber(p Prober) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p != nil {
		m.prober = p
	}
}
func (m *Manager) TestURL(ctx context.Context, address string) (*Entry, error) {
	return m.testURL(ctx, address, "manual_probe")
}

func (m *Manager) testURL(ctx context.Context, address, source string) (entry *Entry, resultErr error) {
	defer func() { logOperationError(source, address, "", "", resultErr) }()
	e, err := newEndpoint(address)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return nil, ErrClosed
	}
	s := m.state(e.String(), "")
	if s.Probing || s.InFlight > 0 {
		m.mu.Unlock()
		return nil, ErrUnavailable
	}
	s.Probing = true
	p := m.prober
	m.probes.Add(1)
	m.mu.Unlock()
	defer m.probes.Done()
	ctx, cancel := context.WithTimeout(ctx, m.policy.ProbeTimeout)
	defer cancel()
	stopCancel := context.AfterFunc(m.ctx, cancel)
	defer stopCancel()
	err = p(ctx, e)
	m.mu.Lock()
	defer m.mu.Unlock()
	s = m.state(e.String(), "")
	s.Probing = false
	if m.ctx.Err() != nil || ctx.Err() == context.Canceled {
		cancelErr := ctx.Err()
		if cancelErr == nil {
			cancelErr = m.ctx.Err()
		}
		return nil, cancelErr
	}
	now := m.now().UTC()
	s.LastProbe = now
	s.ProbeRequests++
	result := &Entry{URL: e.String(), Status: "unavailable"}
	if err == nil {
		m.markSucceeded(stateKey{e.String(), ""}, now, source)
		result.Status = s.Status
		result.Available = s.Status == "available"
		result.LastAvailable = &now
	} else {
		s.ProbeFailures++
		logOperationError(source, e.String(), "", "", err)
		m.markFailed(stateKey{e.String(), ""}, now, source, true)
		result.LastErrorAt = &now
	}
	return result, nil
}

// Automatic work is network-only. Application recovery never manufactures
// requests, and all tasks are owned/canceled/waited by Manager.Close.
func (m *Manager) startDueProbes() {
	m.mu.Lock()
	if m.stopped || !m.policy.AutoProbe {
		m.mu.Unlock()
		return
	}
	groups, err := m.store.ListProxyGroups()
	if err != nil {
		m.mu.Unlock()
		logOperationError("automatic_probe_scan", "", "", "", err)
		return
	}
	active := map[string]bool{}
	for _, g := range groups {
		if g.Enabled != nil && !*g.Enabled {
			continue
		}
		for _, e := range g.Proxies {
			if e.Enabled {
				active[e.URL] = true
			}
		}
	}
	running := 0
	for k, s := range m.states {
		if k.app == "" && s.Probing {
			running++
		}
	}
	var addresses []string
	for k, s := range m.states {
		if len(addresses)+running >= m.policy.ProbeConcurrency {
			break
		}
		if k.app == "" && active[k.address] && (s.Status == "unavailable" || s.Status == "half_open") && !s.Probing && s.InFlight == 0 && !m.now().Before(s.CooldownUntil) {
			addresses = append(addresses, k.address)
		}
	}
	// Register launchers before releasing the lifecycle lock; Close cannot race
	// Wait with an unregistered goroutine. TestURL registers its own probe too.
	m.probes.Add(len(addresses))
	m.mu.Unlock()
	for _, address := range addresses {
		go func() { defer m.probes.Done(); _, _ = m.testURL(m.ctx, address, "automatic_probe") }()
	}
}
func (m *Manager) Test(ctx context.Context, groupID, proxyID string) (*Entry, error) {
	groups, err := m.List()
	if err != nil {
		return nil, err
	}
	for _, g := range groups {
		if g.ID == groupID {
			for _, p := range g.Proxies {
				if p.ID == proxyID {
					if _, err := newEndpoint(p.URL); err != nil {
						// List already logged the invalid persisted endpoint.
						return nil, err
					}
					result, err := m.TestURL(ctx, p.URL)
					if err != nil {
						return nil, err
					}
					result.ID = p.ID
					result.Name = p.Name
					result.Enabled = p.Enabled
					return result, nil
				}
			}
		}
	}
	err = errors.New("proxy not found")
	logOperationError("find_proxy", "", groupID, "", err)
	return nil, err
}
func (m *Manager) ErrorRecords(groupID, proxyID string) (*Entry, error) {
	groups, err := m.List()
	if err != nil {
		return nil, err
	}
	for _, g := range groups {
		if g.ID == groupID {
			for _, p := range g.Proxies {
				if p.ID == proxyID {
					e, err := newEndpoint(p.URL)
					if err != nil {
						// List already logged the invalid persisted endpoint.
						return nil, err
					}
					buckets, err := m.History(e, "", time.Time{}, m.now().Add(time.Minute))
					if err != nil {
						return nil, err
					}
					p.ErrorRecords = nil
					for _, b := range buckets {
						if b.Failures > 0 {
							p.ErrorRecords = append(p.ErrorRecords, ErrorRecord{StartAt: b.StartAt, Count: int(b.Failures)})
						}
					}
					return &p, nil
				}
			}
		}
	}
	err = errors.New("proxy not found")
	logOperationError("find_proxy", "", groupID, "", err)
	return nil, err
}
