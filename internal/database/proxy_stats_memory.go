package database

import (
	"ai-unisub/internal/proxy"
	"maps"
	"slices"
	"time"
)

func (m *MemoryDatabase) SaveProxyStats(values []proxy.Bucket) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.proxyStats == nil {
		m.proxyStats = map[proxyStatsKey]proxy.Bucket{}
	}
	for _, b := range values {
		m.proxyStats[proxyStatsKey{b.Address, b.Application, b.Source, b.StartAt}] = b
	}
	return nil
}

type proxyStatsKey struct {
	address, app, source string
	at                   time.Time
}

func (m *MemoryDatabase) ListProxyStats(address, app string, from, to time.Time) ([]proxy.Bucket, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	aggregates := map[time.Time]proxy.Bucket{}
	for b := range maps.Values(m.proxyStats) {
		if b.Address == address && b.Application == app && !b.StartAt.Before(from) && b.StartAt.Before(to) {
			v := aggregates[b.StartAt]
			v.Address = address
			v.Application = app
			v.StartAt = b.StartAt
			v.Requests += b.Requests
			v.Failures += b.Failures
			aggregates[b.StartAt] = v
		}
	}
	out := slices.AppendSeq([]proxy.Bucket{}, maps.Values(aggregates))
	return out, nil
}
