package proxy

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestWindowRateAndIndependentApplicationRecovery(t *testing.T) {
	m, _, now := setup(t)
	m.policy.NetworkFailureThreshold = 10
	m.policy.ApplicationFailureThreshold = 10
	m.policy.MinSamples = 5
	m.policy.ApplicationFailureRate = .5
	m.policy.ApplicationCooldown = 5 * time.Minute
	m.policy.ApplicationRecoverySuccesses = 2
	saveGroup(t, m, "g", "http://localhost:8001")
	e := resolve(t, m, "g", "claude")
	for _, class := range []ErrorClass{ApplicationError, Success, ApplicationError, Success, ApplicationError} {
		if err := m.ReportProxy(e, "claude", class); err != nil {
			t.Fatal(err)
		}
	}
	if m.Snapshot(e, "claude").Status != "unavailable" {
		t.Fatal("window threshold did not trip")
	}
	if m.Snapshot(e, "").Status != "available" {
		t.Fatal("application failure broke network")
	}
	if _, err := m.ResolveProxy(context.Background(), "g", "codex", nil); err != nil {
		t.Fatal("other application quarantined", err)
	}
	m.SetProber(func(context.Context, *Endpoint) error { return nil })
	_, _ = m.TestURL(context.Background(), e.String())
	if m.Snapshot(e, "").Requests != 5 || m.Snapshot(e, "").ProbeRequests != 1 {
		t.Fatal("probe mixed into business statistics")
	}
	*now = now.Add(11 * time.Minute)
	if len(m.Recent(e, "claude")) != 0 || m.Snapshot(e, "claude").Status != "unavailable" {
		t.Fatal("bucket expiry must not recover application")
	}
	half := resolve(t, m, "g", "claude")
	_ = m.ReportProxy(half, "claude", Success)
	if m.Snapshot(e, "claude").Status != "half_open" {
		t.Fatal("one success recovered too early")
	}
	half = resolve(t, m, "g", "claude")
	_ = m.ReportProxy(half, "claude", Canceled)
	if m.Snapshot(e, "claude").RecoverySuccesses != 1 {
		t.Fatal("cancellation changed recovery")
	}
	half = resolve(t, m, "g", "claude")
	_ = m.ReportProxy(half, "claude", Success)
	if m.Snapshot(e, "claude").Status != "available" {
		t.Fatal("did not recover")
	}
	before := m.Snapshot(e, "claude").Requests
	_ = m.ReportProxy(e, "claude", NetworkError)
	if m.Snapshot(e, "claude").Requests != before {
		t.Fatal("network failure contaminated application denominator")
	}
}

func TestAutomaticProbesAreNetworkOnlyAndRecoverInStages(t *testing.T) {
	m, _, now := setup(t)
	m.policy.NetworkRecoverySuccesses = 2
	saveGroup(t, m, "g", "http://localhost:8001", "http://localhost:8002")
	first := resolve(t, m, "g", "a")
	_ = m.ReportProxy(first, "a", ApplicationError)
	second, err := m.ResolveProxy(context.Background(), "g", "a", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = m.ReportProxy(second, "a", NetworkError)
	*now = now.Add(2 * time.Minute)
	var count atomic.Int32
	m.SetProber(func(_ context.Context, e *Endpoint) error {
		if e.String() == first.String() {
			t.Error("application failure actively probed")
		}
		count.Add(1)
		return nil
	})
	m.mu.Lock()
	m.policy.AutoProbe = true
	m.mu.Unlock()
	m.startDueProbes()
	m.probes.Wait()
	if count.Load() != 1 || m.Snapshot(second, "").Status != "half_open" {
		t.Fatal("first probe did not half-open")
	}
	m.startDueProbes()
	m.probes.Wait()
	if count.Load() != 2 || m.Snapshot(second, "").Status != "available" {
		t.Fatal("network recovery failed")
	}
	if m.Snapshot(first, "a").Status != "unavailable" {
		t.Fatal("application auto-recovered")
	}
}

func TestNetworkProbeBackoffAndApplicationNoTraffic(t *testing.T) {
	m, _, now := setup(t)
	saveGroup(t, m, "g", "http://localhost:8001")
	e := resolve(t, m, "g", "a")
	_ = m.ReportProxy(e, "a", NetworkError)
	first := m.Snapshot(e, "").CooldownUntil.Sub(*now)
	*now = now.Add(time.Minute)
	m.SetProber(func(context.Context, *Endpoint) error { return errors.New("offline") })
	_, _ = m.TestURL(context.Background(), e.String())
	if delay := m.Snapshot(e, "").CooldownUntil.Sub(*now); delay <= first || delay > m.policy.MaxProbeInterval {
		t.Fatal("backoff not bounded/increasing", delay)
	}
}
