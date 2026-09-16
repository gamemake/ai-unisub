package proxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testStore struct {
	groups  []Group
	buckets map[string]Bucket
	fail    bool
}

func (s *testStore) ListProxyGroups() ([]Group, error) { return clone(s.groups), nil }
func (s *testStore) SaveProxyGroup(g *Group) error {
	for i := range s.groups {
		if s.groups[i].ID == g.ID {
			s.groups[i] = clone(*g)
			return nil
		}
	}
	s.groups = append(s.groups, clone(*g))
	return nil
}
func (s *testStore) DeleteProxyGroup(id string) error { return nil }
func (s *testStore) SaveProxyStats(v []Bucket) error {
	if s.fail {
		return errors.New("storage unavailable")
	}
	if s.buckets == nil {
		s.buckets = map[string]Bucket{}
	}
	for _, b := range v {
		s.buckets[b.Address+"|"+b.Application+"|"+b.StartAt.String()+"|"+b.Source] = b
	}
	return nil
}
func (s *testStore) ListProxyStats(address, app string, from, to time.Time) ([]Bucket, error) {
	var out []Bucket
	for _, b := range s.buckets {
		if b.Address == address && b.Application == app && !b.StartAt.Before(from) && b.StartAt.Before(to) {
			out = append(out, b)
		}
	}
	return out, nil
}
func setup(t *testing.T) (*Manager, *testStore, *time.Time) {
	t.Helper()
	store := &testStore{}
	policy := DefaultPolicy()
	policy.FailureThreshold = 1
	// These contract tests use immediate, single-success recovery. Dedicated
	// policy tests exercise production cooldowns and multi-success recovery.
	policy.ApplicationCooldown = time.Minute
	policy.NetworkRecoverySuccesses = 1
	policy.ApplicationRecoverySuccesses = 1
	policy.AutoProbe = false
	m := NewManager(store, policy)
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	t.Cleanup(func() {
		store.fail = false
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	return m, store, &now
}
func saveGroup(t *testing.T, m *Manager, id string, addresses ...string) {
	t.Helper()
	g := Group{ID: id, Name: id, MaxRetries: 3}
	for _, a := range addresses {
		g.Proxies = append(g.Proxies, Entry{URL: a, Enabled: true})
	}
	if err := m.Save(&g); err != nil {
		t.Fatal(err)
	}
}
func resolve(t *testing.T, m *Manager, group, app string) *Endpoint {
	t.Helper()
	e, err := m.ResolveProxy(t.Context(), group, app, nil)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func TestPrioritySharedHealthAndApplicationIsolation(t *testing.T) {
	m, _, now := setup(t)
	saveGroup(t, m, "one", "http://LOCALHOST:8001/", "http://localhost:8002")
	saveGroup(t, m, "two", "http://localhost:8001")
	first := resolve(t, m, "one", "app-a")
	if first.String() != "http://localhost:8001" {
		t.Fatal(first)
	}
	if again := resolve(t, m, "one", "app-a"); again.String() != first.String() {
		t.Fatal("selection must not round robin")
	}
	if err := m.ReportProxy(first, "app-a", ApplicationError); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ResolveProxy(t.Context(), "two", "app-a", nil); !errors.Is(err, ErrUnavailable) {
		t.Fatal("application failure not shared across groups")
	}
	if other := resolve(t, m, "two", "app-b"); other.String() != first.String() {
		t.Fatal("application failure leaked")
	}
	m.SetProber(func(context.Context, *Endpoint) error { return nil })
	if _, err := m.TestURL(t.Context(), first.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ResolveProxy(t.Context(), "two", "app-a", nil); !errors.Is(err, ErrUnavailable) {
		t.Fatal("network probe cleared application failure")
	}
	*now = now.Add(2 * time.Minute)
	recovered := resolve(t, m, "two", "app-a")
	if err := m.ReportProxy(recovered, "app-a", Success); err != nil {
		t.Fatal(err)
	}
	if err := m.ReportProxy(first, "app-a", NetworkError); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ResolveProxy(t.Context(), "two", "app-b", nil); !errors.Is(err, ErrUnavailable) {
		t.Fatal("network failure not global")
	}
	if m.Snapshot(first, "app-a").Status != "available" {
		t.Fatal("network failure changed application health")
	}
	second := resolve(t, m, "one", "app-b")
	if second.String() == first.String() {
		t.Fatal("did not fail over")
	}
}
func TestHalfOpenQuotaCancellationAndNoBypass(t *testing.T) {
	m, _, now := setup(t)
	saveGroup(t, m, "one", "http://localhost:8001")
	e := resolve(t, m, "one", "a")
	_ = m.ReportProxy(e, "a", NetworkError)
	*now = now.Add(2 * time.Minute)
	var admitted atomic.Int32
	leases := make(chan *Endpoint, 20)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if selected, err := m.ResolveProxy(t.Context(), "one", "a", nil); err == nil {
				admitted.Add(1)
				leases <- selected
			}
		})
	}
	wg.Wait()
	if admitted.Load() != 1 {
		t.Fatalf("half-open admitted %d", admitted.Load())
	}
	_ = m.ReportProxy(e, "a", Canceled)
	if _, err := m.ResolveProxy(t.Context(), "one", "a", nil); !errors.Is(err, ErrUnavailable) {
		t.Fatal("unrelated cancellation released a half-open lease")
	}
	_ = m.ReportProxy(<-leases, "a", Canceled)
	e = resolve(t, m, "one", "a")
	_ = m.ReportProxy(e, "a", Success)
	if _, err := m.ResolveProxy(t.Context(), "one", "a", []string{e.String()}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("retried an attempted address")
	}
	if direct, err := m.ResolveProxy(t.Context(), "", "a", nil); err != nil || direct != nil {
		t.Fatal("empty group must leave proxy unspecified")
	}
	if _, err := m.ResolveProxy(t.Context(), "missing", "a", nil); err == nil {
		t.Fatal("missing group silently bypassed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := m.ResolveProxy(ctx, "one", "a", nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestStatsWindowsFailureRetryAndCapacity(t *testing.T) {
	m, store, now := setup(t)
	m.policy.MaxBuckets = 2
	saveGroup(t, m, "one", "http://localhost:8001")
	e := resolve(t, m, "one", "a")
	_ = m.ReportProxy(e, "a", ApplicationError)
	store.fail = true
	if err := m.Flush(); err == nil {
		t.Fatal("write failure swallowed")
	}
	if len(m.pending) != 2 {
		t.Fatal("lost pending data")
	}
	*now = now.Add(11 * time.Minute)
	if len(m.Recent(e, "a")) != 0 {
		t.Fatal("expired minute bucket retained")
	}
	if err := m.ReportProxy(e, "a", Success); err == nil {
		t.Fatal("unbounded accumulation on failed persistence")
	}
	if len(m.pending) > 2 {
		t.Fatal("capacity exceeded")
	}
	store.fail = false
	if err := m.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := m.Flush(); err != nil {
		t.Fatal(err)
	}
	history, err := m.History(e, "a", now.Add(-time.Hour), *now)
	if err != nil || len(history) != 1 || history[0].Requests != 1 || history[0].Failures != 1 {
		t.Fatalf("bad aggregate: %+v %v", history, err)
	}
	if err := m.ReportProxy(e, "a", Success); err != nil {
		t.Fatal(err)
	}
	_ = m.Flush()
	_ = m.Flush()
	history, _ = m.History(e, "a", now.Add(-time.Hour), now.Add(time.Hour))
	var total int64
	for _, b := range history {
		total += b.Requests
	}
	if total != 2 {
		t.Fatalf("duplicate aggregate: %d", total)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestEndpointValidationAndClientIsolation(t *testing.T) {
	for _, address := range []string{"", "localhost:80", "http://", "http://:80", "ftp://localhost", "http://localhost:0", "http://localhost:65536", "http://localhost:", "http://localhost?", "http://localhost#", "http://local host", "http:opaque"} {
		if _, err := NewEndpoint(address); err == nil {
			t.Errorf("accepted %q", address)
		}
	}
	a, _ := NewEndpoint(" HTTP://user:pass@LOCALHOST:80/ ")
	b, _ := NewEndpoint("http://user:other@localhost")
	if a.String() == b.String() {
		t.Fatal("merged distinct credentials")
	}
	for _, scheme := range []string{"http", "https", "socks5", "socks5h"} {
		if _, err := NewEndpoint(scheme + "://[::1]:8080"); err != nil {
			t.Fatal(err)
		}
	}
	base := &http.Client{Timeout: 3 * time.Second, Transport: http.DefaultTransport.(*http.Transport).Clone()}
	if Client(base, nil) != base {
		t.Fatal("nil endpoint changed client")
	}
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Go(func() {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(210 + i) }))
			defer srv.Close()
			e, _ := NewEndpoint(srv.URL)
			client := Client(base, e)
			defer client.CloseIdleConnections()
			resp, err := client.Get("http://destination.invalid/")
			if err != nil {
				t.Error(err)
				return
			}
			resp.Body.Close()
			if resp.StatusCode != 210+i || client.Timeout != base.Timeout {
				t.Error("proxy client isolation failed")
			}
		})
	}
	wg.Wait()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://destination.invalid", nil)
	if _, err := Client(base, a).Do(req); !errors.Is(err, context.Canceled) {
		t.Fatal("context was not propagated", err)
	}
}

func TestCloseCancelsProbeAndCanRetryPersistence(t *testing.T) {
	m, store, _ := setup(t)
	started := make(chan struct{})
	finished := make(chan error, 1)
	m.SetProber(func(ctx context.Context, e *Endpoint) error { close(started); <-ctx.Done(); return ctx.Err() })
	go func() { _, err := m.TestURL(t.Context(), "http://localhost:8080"); finished <- err }()
	<-started
	e, _ := NewEndpoint("http://localhost:8080")
	if err := m.ReportProxy(e, "a", Success); err != nil {
		t.Fatal(err)
	}
	store.fail = true
	if err := m.Close(); err == nil {
		t.Fatal("close swallowed failed persistence")
	}
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal("close did not cancel probe", err)
	}
	store.fail = false
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
}
