package aiprovider

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNativeSessionHeaders(t *testing.T) {
	for _, tc := range []struct{ ua, header, value, want string }{
		{"claude-cli/2.1.220 (external, cli)", "x-claude-code-session-id", "claude-session", "claude-session"},
		{"codex_cli_rs/0.120.0 (Windows)", "session_id", "codex-session", "codex-session"},
		{"codex-tui/0.146.0", "session-id", "codex-session", "codex-session"},
		{"codex/1.0", "x-session-id", "codex-session", "codex-session"},
		{"grok-shell/1.0.5 (linux; x86_64)", "x-grok-session-id", "grok-session", "grok-session"},
		{"xai-grok-workspace/1.0", "x-grok-conv-id", "grok-conversation", "grok-conversation"},
		{"grok-shell/1.0", "x-grok-req-id", "request", ""},
		{"grok-shell/1.0", "x-claude-code-session-id", "wrong-client", ""},
		{"curl/8.0", "session_id", "unknown", ""},
		{"curl/8.0", "x-unisub-session-id", "explicit", ""},
		{"codex/1.0", "x-unisub-session-id", "explicit", ""},
	} {
		t.Run(tc.ua+tc.header, func(t *testing.T) {
			h := http.Header{}
			h.Set("User-Agent", tc.ua)
			h.Set(tc.header, tc.value)
			got, err := SessionID(h)
			if err != nil || got != tc.want {
				t.Fatalf("got %q %v", got, err)
			}
		})
	}
	h := http.Header{}
	h.Set("User-Agent", "grok-shell/1.0")
	h.Set("X-Grok-Session-Id", "session")
	h.Set("X-Grok-Conv-Id", "conversation")
	h.Set("X-Unisub-Session-ID", "override")
	if got, _ := SessionID(h); got != "session" {
		t.Fatal(got)
	}
	h.Del("X-Unisub-Session-ID")
	if got, _ := SessionID(h); got != "session" {
		t.Fatal(got)
	}
	for _, value := range []string{"", "a,b", "bad\nvalue", strings.Repeat("a", 129)} {
		h.Set("X-Grok-Session-Id", value)
		if _, err := SessionID(h); err == nil {
			t.Fatalf("accepted invalid %q", value)
		}
	}
	h.Set("X-Grok-Session-Id", "one")
	h.Add("X-Grok-Session-Id", "two")
	if _, err := SessionID(h); err == nil {
		t.Fatal("accepted multiple values")
	}
	h.Set("User-Agent", "claude-cli/1.0 codex/1.0")
	if DetectClient(h) != "" {
		t.Fatal("ambiguous client accepted")
	}
}

func routingManager(t *testing.T) *AIProviderManager {
	t.Helper()
	m := NewAIProviderManager()
	if err := m.Register("dummy", DummyAIProviderFactory(nil)); err != nil {
		t.Fatal(err)
	}
	return m
}
func createRouting(t *testing.T, m *AIProviderManager, id int, kind, raw string) {
	t.Helper()
	if _, err := m.Create(id, kind, json.RawMessage(raw), nil); err != nil {
		t.Fatal(err)
	}
}
func TestGroupRelationsWeightAffinityAndIsolation(t *testing.T) {
	m := routingManager(t)
	const a, b, b2, g, nested, restricted, c, user, other = 1, 2, 3, 4, 5, 6, 7, 10, 11
	createRouting(t, m, a, "dummy", `{"client_type":"Anthropic"}`)
	createRouting(t, m, b, "dummy", `{"client_type":"OpenAI"}`)
	createRouting(t, m, b2, "dummy", `{"client_type":"OpenAI"}`)
	createRouting(t, m, g, "group", `{"client_type":"OpenAI","members":[{"id":2,"weight":5},{"id":3}]}`)
	p, _ := m.Get(g)
	if p.Config().Members[1].Weight != 3 {
		t.Fatal("default weight")
	}
	if _, e := m.Create(nested, "group", json.RawMessage(`{"members":[{"id":4}]}`), nil); e == nil {
		t.Fatal("nested group accepted")
	}
	createRouting(t, m, restricted, "group", `{"client_type":"Anthropic","members":[{"id":1}]}`)
	if e := m.UpdateConfig(a, json.RawMessage(`{"client_type":"Any"}`)); e == nil {
		t.Fatal("incompatible member update accepted")
	}
	if e := m.UpdateConfig(g, json.RawMessage(`{"client_type":"Anthropic","members":[{"id":1},{"id":2}]}`)); e == nil {
		t.Fatal("incompatible group update accepted")
	}
	h := http.Header{}
	h.Set("User-Agent", "codex-tui/1.0")
	h.Set("Session-Id", "same")
	selected, err := m.Select(g, user, h, "/v1/responses", nil)
	if err != nil || selected.ID != b {
		t.Fatalf("filtered selection %v %v", selected.ID, err)
	}
	if _, err := m.Select(a, user, h, "/v1/messages", nil); !errors.Is(err, ErrClientDenied) {
		t.Fatal("direct policy bypass", err)
	}
	createRouting(t, m, c, "dummy", `{"client_type":"OpenAI"}`)
	if e := m.UpdateConfig(g, json.RawMessage(`{"client_type":"OpenAI","members":[{"id":2,"weight":3},{"id":7,"weight":3}]}`)); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	ids := make(chan int, 40)
	for range 40 {
		wg.Go(func() {
			s, e := m.Select(g, user, h, "/v1/responses", nil)
			if e != nil {
				t.Error(e)
				return
			}
			ids <- s.ID
		})
	}
	wg.Wait()
	close(ids)
	first := 0
	for id := range ids {
		if first == 0 {
			first = id
		}
		if first != id {
			t.Fatal("concurrent session split")
		}
	}
	s, _ := m.Select(g, other, h, "/v1/responses", nil)
	if s.binding == selected.binding {
		t.Fatal("user scope collided")
	}
	current, _ := m.Select(g, user, h, "/v1/responses", nil)
	m.ReportSelection(current, &AIProviderCallTrace{ResponseStatus: 429, ResponseHeaders: http.Header{"Retry-After": []string{"120"}}}, false)
	next, e := m.Select(g, user, h, "/v1/responses", nil)
	if e != nil || next.ID == current.ID {
		t.Fatal("rate-limited member selected")
	}
	m.mu.Lock()
	m.health[current.ID].until = time.Now().Add(-time.Second)
	m.mu.Unlock()
}
func TestCatalogProtectedSuppliers(t *testing.T) {
	m := routingManager(t)
	c := m.Catalog()
	c.Suppliers = c.Suppliers[:5]
	if m.SetCatalog(c) == nil {
		t.Fatal("deleted built-in")
	}
}

func TestGroupClientTypeMustMatchEveryMember(t *testing.T) {
	for _, groupClient := range []ClientType{ClientAny, ClientAnthropic, ClientOpenAI, ClientGrok} {
		for _, memberClient := range []ClientType{ClientAny, ClientAnthropic, ClientOpenAI, ClientGrok} {
			t.Run(string(groupClient)+"/"+string(memberClient), func(t *testing.T) {
				m := routingManager(t)
				raw, _ := json.Marshal(map[string]any{"client_type": memberClient})
				createRouting(t, m, 1, "dummy", string(raw))
				raw, _ = json.Marshal(map[string]any{"client_type": groupClient, "members": []map[string]any{{"id": 1}}})
				_, err := m.Create(2, "group", raw, nil)
				if (err == nil) != (groupClient == memberClient) {
					t.Fatalf("group %s, member %s: %v", groupClient, memberClient, err)
				}
				if err == nil {
					other := ClientAny
					if memberClient == ClientAny {
						other = ClientOpenAI
					}
					raw, _ = json.Marshal(map[string]any{"client_type": other})
					if err := m.UpdateConfig(1, raw); err == nil {
						t.Fatal("member update broke group consistency")
					}
				}
			})
		}
	}
}
