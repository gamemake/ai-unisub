package unisub

import (
	"ai-unisub/internal/database"
	"ai-unisub/internal/service"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func addRoutingProvider(t *testing.T, s *service.Service, cookie *http.Cookie, name, kind string, c any) int {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"name": name, "provider": kind, "config": c})
	w := appRequest(s, "POST", "/api/ai-providers", string(raw), cookie)
	if w.Code != 201 {
		t.Fatalf("create %s: %d %s", name, w.Code, w.Body.String())
	}
	var a database.PersistedAccount
	_ = json.Unmarshal(w.Body.Bytes(), &a)
	return a.ID
}
func routingKey(t *testing.T, s *service.Service, cookie *http.Cookie, id int) string {
	t.Helper()
	w := appRequest(s, "POST", "/api/keys", fmt.Sprintf(`{"name":"routing","account_id":%d}`, id), cookie)
	var k database.PersistedAPIKey
	_ = json.Unmarshal(w.Body.Bytes(), &k)
	if k.Key == "" {
		t.Fatal(w.Body.String())
	}
	return k.Key
}
func TestGatewayGroupNativeAffinityAndRestrictions(t *testing.T) {
	s := testApp(t)
	cookie := loginTestApp(t, s)
	var a, b atomic.Int32
	server := func(counter *atomic.Int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			counter.Add(1)
			body, _ := io.ReadAll(r.Body)
			var v map[string]any
			_ = json.Unmarshal(body, &v)
			if v["model"] != "incoming" {
				t.Errorf("model changed: %s", body)
			}
			if r.Header.Get("Authorization") != "Bearer upstream" || r.Header.Get("Session-Id") != "native-session" || r.Header.Get("X-Unisub-Session-ID") != "" {
				t.Errorf("bad headers: %v", r.Header)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"model":"incoming","ok":true}`)
		}))
	}
	first, second := server(&a), server(&b)
	defer first.Close()
	defer second.Close()
	config := func(url string) map[string]any {
		return map[string]any{"auth_type": "api_key", "api_key": "upstream", "supplier": "openai", "api_endpoint": url + "/v1", "client_type": "OpenAI"}
	}
	id1 := addRoutingProvider(t, s, cookie, "first", "api", config(first.URL))
	id2 := addRoutingProvider(t, s, cookie, "second", "api", config(second.URL))
	group := addRoutingProvider(t, s, cookie, "group", "group", map[string]any{"client_type": "OpenAI", "members": []map[string]any{{"id": id1}, {"id": id2}}})
	key := routingKey(t, s, cookie, group)
	for range 5 {
		r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"incoming","input":"hello"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("User-Agent", "codex-tui/0.146.0")
		r.Header.Set("Session-Id", "native-session")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("gateway %d %s", w.Code, w.Body.String())
		}
	}
	if !(a.Load() == 5 && b.Load() == 0 || a.Load() == 0 && b.Load() == 5) {
		t.Fatal("session was not sticky", a.Load(), b.Load())
	}
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{}`))
	r.Header.Set("Authorization", "Bearer "+key)
	r.Header.Set("User-Agent", "claude-cli/2.1.220")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("client restriction bypassed", w.Code)
	}
	if removed := appRequest(s, "DELETE", "/api/ai-providers/"+pathID(id1), "", cookie); removed.Code != 400 {
		t.Fatal("deleted referenced member")
	}
	traces, count, err := s.Database().QueryCallTraces(database.CallTraceFilter{}, 1, 10)
	if err != nil || count != 5 || traces[0].AccountID == group || traces[0].SessionID != "native-session" {
		t.Fatal("member attribution missing", count, err, traces)
	}
}
func TestGatewaySafeFailoverAndNoReplayAfterUpstreamExecution(t *testing.T) {
	for _, unsafe := range []bool{false, true} {
		t.Run(fmt.Sprint(unsafe), func(t *testing.T) {
			s := testApp(t)
			cookie := loginTestApp(t, s)
			var primaryCalls, fallbackCalls atomic.Int32
			first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				primaryCalls.Add(1)
				w.WriteHeader(503)
				_, _ = w.Write([]byte(`{"error":"temporary"}`))
			}))
			defer first.Close()
			url := first.URL
			if !unsafe {
				first.Close()
			}
			second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fallbackCalls.Add(1)
				w.WriteHeader(200)
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer second.Close()
			config := func(u string) map[string]any {
				return map[string]any{"auth_type": "api_key", "api_key": "key", "supplier": "openai", "api_endpoint": u}
			}
			a := addRoutingProvider(t, s, cookie, "a", "api", config(url))
			b := addRoutingProvider(t, s, cookie, "b", "api", config(second.URL))
			g := addRoutingProvider(t, s, cookie, "g", "group", map[string]any{"members": []map[string]any{{"id": a, "weight": 5}, {"id": b, "weight": 1}}})
			key := routingKey(t, s, cookie, g)
			r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"test"}`))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if unsafe {
				if w.Code != 503 || primaryCalls.Load() != 1 || fallbackCalls.Load() != 0 {
					t.Fatal("replayed executed request", w.Code, primaryCalls.Load(), fallbackCalls.Load())
				}
			} else if w.Code != 200 || fallbackCalls.Load() != 1 {
				t.Fatal("safe failover failed", w.Code, w.Body.String())
			}
		})
	}
}
