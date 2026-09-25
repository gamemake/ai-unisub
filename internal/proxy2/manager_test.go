package proxy2

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ai-unisub/internal/database"
)

func TestProxyManagerRetriesAndPersistsHealth(t *testing.T) {
	db := openTestDatabase(t)
	var firstCalls, secondCalls atomic.Int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		firstCalls.Add(1)
		if body, _ := io.ReadAll(req.Body); string(body) != "payload" {
			t.Errorf("first proxy received body %q", body)
		}
		http.Error(w, "temporary", http.StatusBadGateway)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		secondCalls.Add(1)
		if body, _ := io.ReadAll(req.Body); string(body) != "payload" {
			t.Errorf("second proxy received body %q", body)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer second.Close()

	manager := openTestManager(t, db, 10*time.Millisecond)
	id, err := manager.Create(ProxyGroupConfig{Name: "primary", Proxies: []string{first.URL, second.URL}})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://upstream.invalid/resource", strings.NewReader("request body must be ignored"))
	if err != nil {
		t.Fatal(err)
	}
	handle := func(response *http.Response) error {
		if response.StatusCode == http.StatusBadGateway {
			return errApplicationDetectedProxyFailure
		}
		return nil
	}
	response, err := manager.Do(id, "openai", req, []byte("payload"), handle)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusCreated || firstCalls.Load() != 1 || secondCalls.Load() != 1 {
		t.Fatalf("unexpected retry result: status=%d first=%d second=%d", response.StatusCode, firstCalls.Load(), secondCalls.Load())
	}

	group := manager.List()[0]
	if group.State.Proxies[first.URL].Applications["openai"].Healthy {
		t.Fatal("failed proxy application state was not persisted")
	}
	logs, total, err := db.QueryProxyLogs(database.ProxyLogFilter{GroupID: id, TimeRange: database.TimeRange{Start: time.Now().Add(-time.Minute), End: time.Now().Add(time.Minute)}}, 1, 10)
	if err != nil || total != 2 || len(logs) != 2 {
		t.Fatalf("unexpected proxy logs: logs=%+v total=%d err=%v", logs, total, err)
	}
	statuses := []int{logs[0].HTTPErrorCode, logs[1].HTTPErrorCode}
	slices.Sort(statuses)
	if !slices.Equal(statuses, []int{http.StatusCreated, http.StatusBadGateway}) {
		t.Fatalf("unexpected proxy log statuses: %v", statuses)
	}

	// The healthy proxy is preferred on the next request while preserving the
	// configured order among candidates with equal health.
	req, _ = http.NewRequestWithContext(t.Context(), http.MethodPost, "http://upstream.invalid/resource", strings.NewReader("payload"))
	response, err = manager.Do(id, "openai", req, []byte("payload"), handle)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if firstCalls.Load() != 1 || secondCalls.Load() != 2 {
		t.Fatalf("health ordering was ignored: first=%d second=%d", firstCalls.Load(), secondCalls.Load())
	}

	// Status codes have no built-in meaning. Without a handler, the first
	// proxy's 502 response is accepted and no retry is performed.
	req, _ = http.NewRequestWithContext(t.Context(), http.MethodPost, "http://upstream.invalid/resource", strings.NewReader("ignored"))
	response, err = manager.Do(id, "status-ignored", req, []byte("payload"), nil)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadGateway || firstCalls.Load() != 2 || secondCalls.Load() != 2 {
		t.Fatalf("HTTP status was interpreted by the manager: status=%d first=%d second=%d", response.StatusCode, firstCalls.Load(), secondCalls.Load())
	}

	deadline := time.Now().Add(time.Second)
	for {
		concrete := manager.(*proxyManager)
		concrete.mu.RLock()
		dirty := concrete.groups[id].dirty
		concrete.mu.RUnlock()
		if !dirty {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("dirty proxy state was not flushed on schedule")
		}
		time.Sleep(5 * time.Millisecond)
	}

	reloaded := openTestManager(t, db, defaultFlushInterval)
	if listed := reloaded.List(); len(listed) != 1 || listed[0].ID != id || listed[0].Config.Name != "primary" || listed[0].State.Proxies[first.URL].Applications["openai"].Healthy {
		t.Fatalf("persisted group was not loaded: %+v", listed)
	}
}

func TestProxyManagerDirectRequestUsesExplicitBody(t *testing.T) {
	db := openTestDatabase(t)
	manager := openTestManager(t, db, defaultFlushInterval)
	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		received <- string(body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL, strings.NewReader("ignored"))
	if err != nil {
		t.Fatal(err)
	}
	response, err := manager.Do(0, "", req, []byte("explicit"), nil)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if body := <-received; body != "explicit" {
		t.Fatalf("request body was not ignored: %q", body)
	}
	logs, total, err := db.QueryProxyLogs(database.ProxyLogFilter{GroupID: 0, TimeRange: database.TimeRange{Start: time.Now().Add(-time.Minute), End: time.Now().Add(time.Minute)}}, 1, 10)
	if err != nil || total != 1 || len(logs) != 1 || logs[0].HTTPErrorCode != http.StatusNoContent {
		t.Fatalf("direct call was not recorded: logs=%+v total=%d err=%v", logs, total, err)
	}
}

func TestProxyManagerCRUD(t *testing.T) {
	db := openTestDatabase(t)
	manager := openTestManager(t, db, defaultFlushInterval)
	id, err := manager.Create(ProxyGroupConfig{Name: "one", Proxies: []string{"http://127.0.0.1:8001/"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Update(id, ProxyGroupConfig{Name: "two", Proxies: []string{"http://127.0.0.1:8002"}}); err != nil {
		t.Fatal(err)
	}
	listed := manager.List()
	if len(listed) != 1 || listed[0].Config.Name != "two" || listed[0].Config.Proxies[0] != "http://127.0.0.1:8002" {
		t.Fatalf("unexpected group: %+v", listed)
	}
	listed[0].Config.Name = "mutated"
	if manager.List()[0].Config.Name != "two" {
		t.Fatal("List exposed manager state")
	}
	if err := manager.Delete(id); err != nil {
		t.Fatal(err)
	}
	if len(manager.List()) != 0 {
		t.Fatal("deleted group is still listed")
	}
}

func openTestManager(t *testing.T, db database.Database, flushInterval time.Duration) ProxyManager {
	t.Helper()
	manager := NewManager(db)
	manager.(*proxyManager).flushInterval = flushInterval
	if err := manager.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Error(err)
		}
	})
	return manager
}

func openTestDatabase(t *testing.T) database.Database {
	t.Helper()
	db, err := database.NewDatabase("sqlite::memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}
