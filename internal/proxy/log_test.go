package proxy

import (
	"ai-unisub/internal/common"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type logBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}

func (b *logBuffer) take() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	raw := b.Buffer.String()
	b.Buffer.Reset()
	var text strings.Builder
	for line := range strings.SplitSeq(strings.TrimSpace(raw), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]string
		if err := json.Unmarshal([]byte(line), &entry); err != nil || len(entry) != 5 || entry["module"] != "proxy" {
			panic(fmt.Sprintf("invalid common log: %s", line))
		}
		fmt.Fprintf(&text, "event=%s %s\n", entry["event"], entry["msg"])
	}
	return text.String()
}

func captureProxyLogs(t *testing.T) *logBuffer {
	t.Helper()
	b := &logBuffer{}
	if err := common.InitLogging(common.LogConfig{Output: b}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = common.InitLogging(common.LogConfig{}) })
	return b
}

func TestFailureLogsWithoutTransitionAndDetailedReporting(t *testing.T) {
	b := captureProxyLogs(t)
	m, _, _ := setup(t)
	m.policy.NetworkFailureThreshold = 3
	e, err := NewEndpoint("http://proxy-user:proxy-password@localhost:8001")
	if err != nil {
		t.Fatal(err)
	}
	cause := &url.Error{Op: "Get", URL: "https://target/private-token?access_token=query-secret", Err: errors.New("connection refused via http://proxy-user:proxy-password@localhost:8001; Authorization: Bearer header-secret")}
	for range 2 {
		if err := ReportResult(m, e, "app", NetworkError, cause, 0); err != nil {
			t.Fatal(err)
		}
	}
	text := b.take()
	if strings.Count(text, "event=proxy_error") != 2 || strings.Contains(text, "event=state_changed") {
		t.Fatalf("failures below threshold must each log without transition: %s", text)
	}
	if !strings.Contains(text, "connection refused") {
		t.Fatalf("missing cause: %s", text)
	}
	for _, secret := range []string{"proxy-user", "proxy-password", "private-token", "query-secret", "header-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("leaked %s: %s", secret, text)
		}
	}
	if err := ReportResult(m, e, "app", ApplicationIgnored, nil, 401); err != nil {
		t.Fatal(err)
	}
	text = b.take()
	if strings.Count(text, "event=proxy_error") != 1 || !strings.Contains(text, "status=401") {
		t.Fatalf("ignored application errors must retain HTTP status: %s", text)
	}
	if err := m.ReportProxy(e, "app", Success); err != nil {
		t.Fatal(err)
	}
	b.take() // First application success initializes its state.
	if err := m.ReportProxy(e, "app", Success); err != nil {
		t.Fatal(err)
	}
	if text := b.take(); text != "" {
		t.Fatalf("stable success should be quiet: %s", text)
	}
}

func TestTransitionLogsAndCancellation(t *testing.T) {
	b := captureProxyLogs(t)
	m, _, now := setup(t)
	saveGroup(t, m, "g", "http://localhost:8001")
	e := resolve(t, m, "g", "app")
	if err := m.ReportProxy(e, "app", ApplicationError); err != nil {
		t.Fatal(err)
	}
	text := b.take()
	if strings.Count(text, "event=proxy_error") != 1 || !strings.Contains(text, "scope=application") || !strings.Contains(text, "from=unknown to=unavailable") {
		t.Fatalf("missing application failure transition: %s", text)
	}
	*now = now.Add(2 * time.Minute)
	e = resolve(t, m, "g", "app")
	text = b.take()
	if strings.Count(text, "event=state_changed") != 1 || !strings.Contains(text, "from=unavailable to=half_open") || !strings.Contains(text, "in_flight=1") {
		t.Fatalf("missing half-open admission: %s", text)
	}
	before := m.Snapshot(e, "app")
	if err := m.ReportProxy(e, "app", Canceled); err != nil {
		t.Fatal(err)
	}
	text = b.take()
	if strings.Count(text, "event=request_canceled") != 1 || strings.Contains(text, "event=proxy_error") || strings.Contains(text, "event=state_changed") {
		t.Fatalf("cancellation misclassified: %s", text)
	}
	after := m.Snapshot(e, "app")
	if after.Failures != before.Failures || after.Status != before.Status || after.InFlight != 0 {
		t.Fatal("cancellation changed health or leaked quota")
	}
	e = resolve(t, m, "g", "app")
	if err := m.ReportProxy(e, "app", Success); err != nil {
		t.Fatal(err)
	}
	text = b.take()
	if strings.Count(text, "event=state_changed") != 1 || !strings.Contains(text, "from=half_open to=available") {
		t.Fatalf("missing recovery: %s", text)
	}
}

func TestProbeLogsPreserveOldStateAndCooldownUpdates(t *testing.T) {
	b := captureProxyLogs(t)
	m, _, now := setup(t)
	m.SetProber(func(context.Context, *Endpoint) error { return errors.New("probe offline") })
	for i := range 2 {
		if _, err := m.TestURL(t.Context(), "http://localhost:8001"); err != nil {
			t.Fatal(err)
		}
		text := b.take()
		if strings.Count(text, "event=operation_failed") != 1 || !strings.Contains(text, "probe offline") {
			t.Fatalf("missing or duplicate probe error: %s", text)
		}
		if i == 0 && (!strings.Contains(text, "from=unknown to=unavailable") || strings.Count(text, "event=state_changed") != 1) {
			t.Fatalf("lost original probe state: %s", text)
		}
		if i == 1 && (!strings.Contains(text, "event=cooldown_updated") || strings.Contains(text, "event=state_changed")) {
			t.Fatalf("cooldown incorrectly logged as transition: %s", text)
		}
		*now = now.Add(time.Minute)
	}
	m.policy.NetworkRecoverySuccesses = 2
	m.SetProber(func(context.Context, *Endpoint) error { return nil })
	for _, expected := range []string{"from=unavailable to=half_open", "from=half_open to=available"} {
		if _, err := m.TestURL(t.Context(), "http://localhost:8001"); err != nil {
			t.Fatal(err)
		}
		text := b.take()
		if strings.Count(text, "event=state_changed") != 1 || !strings.Contains(text, expected) {
			t.Fatalf("missing staged probe recovery: %s", text)
		}
	}
}

type failingLogStore struct{ testStore }

func (*failingLogStore) ListProxyGroups() ([]Group, error) { return nil, errors.New("store offline") }

func TestOperationErrorsLoggedOnce(t *testing.T) {
	b := captureProxyLogs(t)
	m, store, now := setup(t)
	e, _ := NewEndpoint("http://localhost:8001")
	if err := m.ReportProxy(e, "app", Success); err != nil {
		t.Fatal(err)
	}
	b.take()
	store.fail = true
	if _, err := m.History(e, "app", now.Add(-time.Hour), *now); err == nil {
		t.Fatal("expected storage error")
	}
	text := b.take()
	if strings.Count(text, "event=operation_failed") != 1 || !strings.Contains(text, "storage unavailable") {
		t.Fatalf("propagated flush failure logged incorrectly: %s", text)
	}
	store.fail = false
	if _, err := m.ResolveProxy(t.Context(), "missing", "app", nil); err == nil {
		t.Fatal("expected missing group")
	}
	text = b.take()
	if strings.Count(text, "event=operation_failed") != 1 || !strings.Contains(text, "reason=group_not_found") {
		t.Fatalf("missing scheduling reason: %s", text)
	}
	if err := m.Save(&Group{ID: "g", Name: "g", Proxies: []Entry{{URL: "bad-address"}}}); err == nil {
		t.Fatal("expected invalid URL")
	}
	if text := b.take(); strings.Count(text, "event=operation_failed") != 1 {
		t.Fatalf("duplicate validation error: %s", text)
	}
	m.store = &failingLogStore{}
	m.policy.AutoProbe = true
	m.startDueProbes()
	text = b.take()
	if strings.Count(text, "event=operation_failed") != 1 || !strings.Contains(text, "automatic_probe_scan") {
		t.Fatalf("silent background failure: %s", text)
	}
	m.store = store
	m.policy.AutoProbe = false
}

func TestLogTextRedactsCredentialsAndTokens(t *testing.T) {
	for _, input := range []string{
		"dial http://alice:secret@proxy:80/private?token=secret: connection refused",
		"Authorization: Bearer secret\nProxy-Authorization: Basic secret\nCookie: session=secret",
		"access_token=secret; refresh_token=secret; api_key=secret",
		`{"access_token":"secret","password":"secret"}`,
		"dial http://alice:%zz@proxy:80: failed",
	} {
		text := safeLogText(input)
		if strings.Contains(text, "secret") || strings.Contains(text, "alice") || strings.Contains(text, "%zz") {
			t.Fatalf("credentials leaked: %s", text)
		}
	}
}
