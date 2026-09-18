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
			body := fmt.Sprintf(`{"name":"test","provider":%q,"config":{"auth_type":"api_key","api_key":"upstream-secret","api_endpoint":%q}}`, platform, upstream.URL+"/custom/v1")
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
			req := httptest.NewRequest("POST", "/v1/messages?test=1", strings.NewReader(`{"model":"local-test"}`))
			req.Header.Set("Authorization", "Bearer "+key.Key)
			// A generic header without a recognized client must not create a session.
			req.Header.Set("Session-Id", "conversation-test")
			req.AddCookie(cookie)
			result := httptest.NewRecorder()
			s.Handler().ServeHTTP(result, req)
			if result.Code != 201 || result.Header().Get("X-Upstream") != "preserved" || !strings.Contains(result.Body.String(), `"ok":true`) {
				t.Fatalf("forward: %d %s", result.Code, result.Body.String())
			}
			traces, count, err := s.Database().QueryCallTraces(recentCallFilter(), 1, 10)
			if err != nil || count != 1 || traces[0].InputTokens != 12 || traces[0].OutputTokens != 3 || traces[0].SessionID != "" {
				t.Fatalf("trace: %#v count=%d err=%v", traces, count, err)
			}
			if traces[0].HTTPErrorCode != 201 {
				t.Fatalf("success trace should store HTTP status 201, got %d", traces[0].HTTPErrorCode)
			}
			trace, err := s.Database().GetCallTrace(traces[0].StartedAt, traces[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if trace.HTTPErrorCode != 201 {
				t.Fatalf("detail HTTPErrorCode = %d, want 201", trace.HTTPErrorCode)
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

func TestGatewayAcceptsXApiKeyFromClaudeCode(t *testing.T) {
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
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"dummy-model"}`))
	req.Header.Set("X-Api-Key", key.Key)
	req.Header.Set("User-Agent", "claude-cli/2.0")
	result := httptest.NewRecorder()
	s.Handler().ServeHTTP(result, req)
	if result.Code != 200 {
		t.Fatalf("x-api-key: %d %s", result.Code, result.Body.String())
	}
	traces, count, err := s.Database().QueryCallTraces(recentCallFilter(), 1, 10)
	if err != nil || count != 1 || traces[0].APIKey != key.Key {
		t.Fatalf("trace key: %#v count=%d err=%v", traces, count, err)
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
