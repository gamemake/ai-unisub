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
	e, err := NewEndpoint(address)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return nil, ErrClosed
	}
	p := m.prober
	m.probes.Add(1)
	m.mu.Unlock()
	defer m.probes.Done()
	ctx, cancel := context.WithTimeout(ctx, m.policy.ProbeTimeout)
	defer cancel()
	stopCancel := context.AfterFunc(m.ctx, cancel)
	defer stopCancel()
	err = p(ctx, e)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.state(e.String(), "")
	now := m.now().UTC()
	s.LastProbe = now
	if recordErr := m.reserve(e.String(), "", now); recordErr != nil {
		return nil, recordErr
	}
	if recordErr := m.record(stateKey{e.String(), ""}, now, err != nil); recordErr != nil {
		return nil, recordErr
	}
	s.Requests++
	result := &Entry{URL: e.String(), Status: "unavailable"}
	if err == nil {
		s.Status = "available"
		s.ConsecutiveFailures = 0
		s.LastSuccess = now
		s.CooldownUntil = time.Time{}
		result.Status = "available"
		result.Available = true
		result.LastAvailable = &now
	} else {
		s.Failures++
		s.ConsecutiveFailures++
		s.Status = "unavailable"
		s.LastFailure = now
		s.CooldownUntil = now.Add(m.policy.Cooldown)
		result.LastErrorAt = &now
	}
	return result, nil
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
	return nil, errors.New("proxy not found")
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
					e, _ := NewEndpoint(p.URL)
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
	return nil, errors.New("proxy not found")
}
