package proxy

import (
	"errors"
	"sort"
	"time"
)

// ReportProxy is called exactly once for each admitted attempt, including cancellation.
func (m *Manager) ReportProxy(e *Endpoint, app string, class ErrorClass) error {
	if e == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	now := m.now().UTC()
	for _, k := range m.leases[e.lease] {
		s := m.state(k.address, k.app)
		if s.InFlight > 0 {
			s.InFlight--
		}
	}
	delete(m.leases, e.lease)
	if class == Canceled {
		return nil
	}
	if class != Success && class != NetworkError && class != ApplicationError && class != ApplicationIgnored {
		return errors.New("invalid proxy error class")
	}
	keys := []stateKey{{e.String(), ""}}
	if app != "" {
		keys = append(keys, stateKey{e.String(), app})
	}
	if err := m.reserve(e.String(), app, now); err != nil {
		return err
	}
	for _, k := range keys {
		// A network failure never reached the application and is not an
		// application sample (including the failure-rate denominator).
		if k.app != "" && (class == NetworkError || class == ApplicationIgnored) {
			continue
		}
		failed := class == NetworkError || (k.app != "" && class == ApplicationError)
		s := m.state(k.address, k.app)
		s.Requests++
		if err := m.record(k, now, failed); err != nil {
			return err
		}
		if failed {
			s.Failures++
			m.markFailed(k, now)
		} else {
			m.markSucceeded(k, now)
		}
	}
	return nil
}

// Reserve bounded aggregate slots before admitting network work. Failed writes
// retain the pending absolute snapshots and apply backpressure to new series.
func (m *Manager) reserve(address, app string, now time.Time) error {
	keys := []bucketKey{{stateKey{address, ""}, now.UTC().Truncate(10 * time.Minute)}}
	if app != "" {
		keys = append(keys, bucketKey{stateKey{address, app}, now.UTC().Truncate(10 * time.Minute)})
	}
	missing := 0
	for _, k := range keys {
		if _, ok := m.pending[k]; !ok {
			missing++
		}
	}
	if len(m.pending)+missing > m.policy.MaxBuckets {
		if err := m.flushLocked(); err != nil {
			return err
		}
		if len(m.pending)+missing > m.policy.MaxBuckets {
			return ErrStatsCapacity
		}
	}
	for _, k := range keys {
		if _, ok := m.pending[k]; !ok {
			m.pending[k] = Bucket{Address: address, Application: k.app, StartAt: k.at, Source: m.source}
		}
	}
	return nil
}
func (m *Manager) record(k stateKey, now time.Time, failed bool) error {
	m.prune(now)
	at := now.Truncate(time.Minute)
	key := bucketKey{k, at}
	b := m.minutes[key]
	b.Address = k.address
	b.Application = k.app
	b.StartAt = at
	b.Requests++
	if failed {
		b.Failures++
	}
	m.minutes[key] = b
	key = bucketKey{k, now.Truncate(10 * time.Minute)}
	if _, ok := m.pending[key]; !ok && len(m.pending) >= m.policy.MaxBuckets {
		if err := m.flushLocked(); err != nil {
			return err
		}
		if len(m.pending) >= m.policy.MaxBuckets {
			return ErrStatsCapacity
		}
	}
	b = m.pending[key]
	b.Address = k.address
	b.Application = k.app
	b.StartAt = key.at
	b.Source = m.source
	b.Requests++
	if failed {
		b.Failures++
	}
	m.pending[key] = b
	return nil
}
func (m *Manager) prune(now time.Time) {
	for k := range m.minutes {
		if !k.at.After(now.Truncate(time.Minute).Add(-10 * time.Minute)) {
			delete(m.minutes, k)
		}
	}
}
func (m *Manager) Recent(e *Endpoint, app string) []Bucket {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prune(m.now())
	out := []Bucket{}
	for k, b := range m.minutes {
		if k.address == e.String() && k.app == app {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartAt.Before(out[j].StartAt) })
	return out
}
func (m *Manager) History(e *Endpoint, app string, from, to time.Time) ([]Bucket, error) {
	if err := m.Flush(); err != nil {
		return nil, err
	}
	return m.store.ListProxyStats(e.String(), app, from, to)
}
func (m *Manager) Flush() error { m.mu.Lock(); defer m.mu.Unlock(); return m.flushLocked() }
func (m *Manager) flushLocked() error {
	m.prune(m.now())
	if err := m.saveHealthLocked(); err != nil {
		return err
	}
	if len(m.pending) == 0 {
		return nil
	}
	values := make([]Bucket, 0, len(m.pending))
	for _, b := range m.pending {
		values = append(values, b)
	}
	if err := m.store.SaveProxyStats(values); err != nil {
		return err
	}
	for k := range m.pending {
		if k.at.Before(m.now().UTC().Truncate(10 * time.Minute)) {
			delete(m.pending, k)
		}
	}
	return nil
}
