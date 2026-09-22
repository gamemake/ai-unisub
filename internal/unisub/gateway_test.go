package unisub

import (
	"ai-unisub/internal/database"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayAPIKeyForwardingAndConfigEdit(t *testing.T) {
	for _, platform := range []string{"codex", "claude", "grok"} {
		t.Run(platform, func(t *testing.T) {
			supplier := map[string]string{"codex": "openai", "claude": "anthropic", "grok": "grok"}[platform]
			userAgent := map[string]string{"codex": "codex-tui/1.0", "claude": "claude-cli/2.1.220", "grok": "grok-cli/1.0"}[platform]
			sessionHeader := map[string]string{"codex": "Session-Id", "claude": "X-Claude-Code-Session-Id", "grok": "X-Grok-Session-Id"}[platform]
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.RequestURI() != "/custom/v1/messages?test=1" || r.Method != "POST" {
					t.Errorf("upstream request: %s %s", r.Method, r.URL)
				}
				if platform == "claude" {
					if r.Header.Get("X-Api-Key") != "upstream-secret" || r.Header.Get("Authorization") != "" {
						t.Error("Claude API key was not applied")
					}
				} else if r.Header.Get("Authorization") != "Bearer upstream-secret" {
					t.Error("upstream key was not applied")
				}
				if r.Header.Get("Cookie") != "" {
					t.Error("browser cookie forwarded upstream")
				}
				body, _ := io.ReadAll(r.Body)
				if string(body) != `{"model":"local-test"}` {
					t.Errorf("body changed: %s", body)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Upstream", "preserved")
				w.WriteHeader(201)
				_, _ = io.WriteString(w, `{"model":"local-test","usage":{"input_tokens":12,"output_tokens":3},"ok":true}`)
			}))
			defer upstream.Close()
			s := testApp(t)
			cookie := loginTestApp(t, s)
			body := fmt.Sprintf(`{"name":"test","provider":%q,"config":{"auth_type":"api_key","api_key":"upstream-secret","supplier":%q,"api_endpoint":%q}}`, platform, supplier, upstream.URL+"/custom/v1")
			out := appRequest(s, "POST", "/api/ai-providers", body, cookie)
			if out.Code != 201 {
				t.Fatalf("create: %d %s", out.Code, out.Body.String())
			}
			var created database.PersistedAccount
			_ = json.Unmarshal(out.Body.Bytes(), &created)
			// Public edits must not erase the secret, even though the response redacts it.
			out = appRequest(s, "PUT", "/api/ai-providers/"+pathID(created.ID), fmt.Sprintf(`{"name":"renamed","config":{"auth_type":"api_key","api_endpoint":%q,"max_concurrent_connections":2}}`, upstream.URL+"/custom/v1"), cookie)
			if out.Code != 200 {
				t.Fatalf("edit: %d %s", out.Code, out.Body.String())
			}
			keyResponse := appRequest(s, "POST", "/api/keys", fmt.Sprintf(`{"name":"client","account_id":%d}`, created.ID), cookie)
			var key database.PersistedAPIKey
			_ = json.Unmarshal(keyResponse.Body.Bytes(), &key)
			if key.Key == "" {
				t.Fatalf("create key: %s", keyResponse.Body.String())
			}
			if platform != "claude" {
				missing := httptest.NewRequest("POST", "/v1/messages?test=1", strings.NewReader(`{"model":"local-test"}`))
				missing.Header.Set("Authorization", "Bearer "+key.Key)
				missing.Header.Set("User-Agent", userAgent)
				missingResult := httptest.NewRecorder()
				s.Handler().ServeHTTP(missingResult, missing)
				if missingResult.Code != http.StatusBadRequest || !strings.Contains(missingResult.Body.String(), "session ID is required") {
					t.Fatalf("missing session ID: %d %s", missingResult.Code, missingResult.Body.String())
				}
			}
			req := httptest.NewRequest("POST", "/v1/messages?test=1", strings.NewReader(`{"model":"local-test"}`))
			req.Header.Set("Authorization", "Bearer "+key.Key)
			req.Header.Set("User-Agent", userAgent)
			req.Header.Set(sessionHeader, "conversation-test")
			req.AddCookie(cookie)
			result := httptest.NewRecorder()
			s.Handler().ServeHTTP(result, req)
			if result.Code != 201 || result.Header().Get("X-Upstream") != "preserved" || !strings.Contains(result.Body.String(), `"ok":true`) {
				t.Fatalf("forward: %d %s", result.Code, result.Body.String())
			}
			traces, count, err := s.Database().QueryCallTraces(recentCallFilter(), 1, 10)
			if err != nil || count != 1 || traces[0].InputTokens != 12 || traces[0].OutputTokens != 3 || traces[0].SessionID != "conversation-test" {
				t.Fatalf("trace: %#v count=%d err=%v", traces, count, err)
			}
			if traces[0].HTTPErrorCode != 201 {
				t.Fatalf("success trace should store HTTP status 201, got %d", traces[0].HTTPErrorCode)
			}
			if traces[0].URL != "/v1/messages?test=1" {
				t.Fatalf("inbound url = %q", traces[0].URL)
			}
			wantOutbound := upstream.URL + "/custom/v1/messages?test=1"
			if traces[0].OutboundURL != wantOutbound {
				t.Fatalf("outbound url = %q, want %q", traces[0].OutboundURL, wantOutbound)
			}
			trace, err := s.Database().GetCallTrace(traces[0].StartedAt, traces[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if trace.HTTPErrorCode != 201 {
				t.Fatalf("detail HTTPErrorCode = %d, want 201", trace.HTTPErrorCode)
			}
			if trace.OutboundURL != wantOutbound {
				t.Fatalf("detail outbound url = %q, want %q", trace.OutboundURL, wantOutbound)
			}
			encoded, _ := json.Marshal(trace.OutboundRequestHeaders)
			if strings.Contains(string(encoded), "upstream-secret") {
				t.Fatal("upstream secret leaked into trace headers")
			}
			if out := appRequest(s, "POST", "/v1/messages", `{}`, cookie); out.Code != 401 {
				t.Fatalf("session must not authorize API calls: %d", out.Code)
			}
		})
	}
}

func TestGatewayAcceptsAuthTokenAndAPIKey(t *testing.T) {
	s := testApp(t)
	cookie := loginTestApp(t, s)
	out := appRequest(s, "POST", "/api/ai-providers", `{"name":"dummy","provider":"dummy","config":{"enabled":true}}`, cookie)
	if out.Code != 201 {
		t.Fatalf("create: %d %s", out.Code, out.Body.String())
	}
	var account database.PersistedAccount
	_ = json.Unmarshal(out.Body.Bytes(), &account)
	out = appRequest(s, "POST", "/api/keys", fmt.Sprintf(`{"name":"client","account_id":%d}`, account.ID), cookie)
	var key database.PersistedAPIKey
	_ = json.Unmarshal(out.Body.Bytes(), &key)
	if key.Key == "" {
		t.Fatalf("create key: %s", out.Body.String())
	}

	// Claude ANTHROPIC_API_KEY → X-Api-Key only.
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"dummy-model"}`))
	req.Header.Set("X-Api-Key", key.Key)
	req.Header.Set("User-Agent", "claude-cli/2.0")
	req.Header.Set("X-Claude-Code-Session-Id", "auth-token-session")
	result := httptest.NewRecorder()
	s.Handler().ServeHTTP(result, req)
	if result.Code != 200 {
		t.Fatalf("x-api-key: %d %s", result.Code, result.Body.String())
	}

	// Claude ANTHROPIC_AUTH_TOKEN → Authorization Bearer only.
	req = httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"dummy-model"}`))
	req.Header.Set("Authorization", "Bearer "+key.Key)
	req.Header.Set("User-Agent", "claude-cli/2.0")
	req.Header.Set("X-Claude-Code-Session-Id", "auth-token-session")
	result = httptest.NewRecorder()
	s.Handler().ServeHTTP(result, req)
	if result.Code != 200 {
		t.Fatalf("auth token: %d %s", result.Code, result.Body.String())
	}

	// Both headers present; leftover official Bearer must not block a valid X-Api-Key.
	req = httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"dummy-model"}`))
	req.Header.Set("Authorization", "Bearer leftover-official-token")
	req.Header.Set("X-Api-Key", key.Key)
	req.Header.Set("User-Agent", "claude-cli/2.0")
	req.Header.Set("X-Claude-Code-Session-Id", "auth-token-session")
	result = httptest.NewRecorder()
	s.Handler().ServeHTTP(result, req)
	if result.Code != 200 {
		t.Fatalf("fallback to x-api-key: %d %s", result.Code, result.Body.String())
	}

	traces, count, err := s.Database().QueryCallTraces(recentCallFilter(), 1, 10)
	if err != nil || count != 3 {
		t.Fatalf("traces count=%d err=%v", count, err)
	}
	for _, trace := range traces {
		if trace.APIKey != key.Key {
			t.Fatalf("trace key: %#v", trace)
		}
	}
}

func TestGatewayAllowsClaudeDesktopModelDiscoveryWithoutSession(t *testing.T) {
	s := testApp(t)
	cookie := loginTestApp(t, s)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" && r.URL.Path != "/v1/messages" {
			t.Fatalf("upstream request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("X-Api-Key") != "upstream-secret" || r.Header.Get("Authorization") != "" {
			t.Fatalf("unexpected upstream auth headers: %v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/models" {
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"claude-sonnet"}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"probe","type":"message","content":[]}`)
	}))
	defer upstream.Close()

	provider := addRoutingProvider(t, s, cookie, "desktop", "api", map[string]any{
		"auth_type":    "api_key",
		"api_key":      "upstream-secret",
		"supplier":     "anthropic",
		"client_type":  "claude",
		"api_endpoint": upstream.URL + "/v1",
	})
	key := routingKey(t, s, cookie, provider)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("User-Agent", "claude-desktop/1.0.0 (Windows)")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"claude-sonnet"`) {
		t.Fatalf("model discovery: %d %s", w.Code, w.Body.String())
	}

	probe := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-sonnet","max_tokens":1,"messages":[{"role":"user","content":"ping"}]}`))
	probe.Header.Set("Authorization", "Bearer "+key)
	probe.Header.Set("Content-Type", "application/json")
	probe.Header.Set("User-Agent", "cc-switch/3.20.3")
	probeResult := httptest.NewRecorder()
	s.Handler().ServeHTTP(probeResult, probe)
	if probeResult.Code != http.StatusOK {
		t.Fatalf("message probe: %d %s", probeResult.Code, probeResult.Body.String())
	}
}

func TestGatewayStreamsBeforeUpstreamCompletes(t *testing.T) {
	finish := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-finish
		_, _ = io.WriteString(w, "data: last\n\n")
	}))
	defer upstream.Close()
	s := testApp(t)
	cookie := loginTestApp(t, s)
	out := appRequest(s, "POST", "/api/ai-providers", fmt.Sprintf(`{"name":"stream","provider":"grok","config":{"auth_type":"api_key","api_key":"test","api_endpoint":%q}}`, upstream.URL), cookie)
	var account database.PersistedAccount
	_ = json.Unmarshal(out.Body.Bytes(), &account)
	out = appRequest(s, "POST", "/api/keys", fmt.Sprintf(`{"name":"client","account_id":%d}`, account.ID), cookie)
	var key database.PersistedAPIKey
	_ = json.Unmarshal(out.Body.Bytes(), &key)
	gateway := httptest.NewServer(s.Handler())
	defer func() { close(finish); gateway.Close() }()
	req, _ := http.NewRequest("POST", gateway.URL+"/v1/responses", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+key.Key)
	req.Header.Set("User-Agent", "grok-cli/1.0")
	req.Header.Set("X-Grok-Session-Id", "stream-session")
	client := &http.Client{Timeout: 3e9}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data := make([]byte, len("data: first\n\n"))
	if _, err := io.ReadFull(response.Body, data); err != nil || string(data) != "data: first\n\n" {
		t.Fatalf("first event was buffered: %q %v", data, err)
	}
	// Test completion/cancellation must release the upstream and gateway before Close.
}
