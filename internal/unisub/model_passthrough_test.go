package unisub

import (
	"ai-unisub/internal/aiprovider"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayPreservesClientModelNames(t *testing.T) {
	for _, tc := range []struct{ client, agent, path, model string }{
		{"claude", "claude-cli/2.1.220", "/v1/messages", "claude-sonnet-5"},
		{"codex", "codex-tui/0.146.0", "/v1/responses", "gpt-5.6-luna"},
		{"grok", "grok-cli/1.0", "/v1/chat/completions", "grok-build-0.1"},
	} {
		t.Run(tc.client, func(t *testing.T) {
			s := testApp(t)
			cookie := loginTestApp(t, s)
			body := "{\n  \"model\": \"" + tc.model + "\", \"input\": \"hello\", \"metadata\": {\"model\": \"untouched\"}\n}"
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, err := io.ReadAll(r.Body)
				if err != nil || string(got) != body {
					t.Errorf("request body changed: %q, %v", got, err)
				}
				if tc.client == "grok" && r.Header.Get("X-Grok-Model-Override") != tc.model {
					t.Error("Grok model header changed")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"ok":true}`)
			}))
			defer upstream.Close()
			supplier := "deepseek"
			if tc.client == "claude" {
				supplier = "anthropic"
			}
			provider := addRoutingProvider(t, s, cookie, "direct", "api", map[string]any{"supplier": supplier, "api_key": "test-key", "api_endpoint": upstream.URL})
			group := addRoutingProvider(t, s, cookie, "group", "group", map[string]any{"members": []map[string]any{{"id": provider}}})
			for _, id := range []int{provider, group} {
				key := routingKey(t, s, cookie, id)
				r := httptest.NewRequest("POST", tc.path, strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+key)
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("User-Agent", tc.agent)
				if tc.client == "grok" {
					r.Header.Set("X-Grok-Model-Override", tc.model)
				}
				w := httptest.NewRecorder()
				s.Handler().ServeHTTP(w, r)
				if w.Code != 200 {
					t.Fatalf("gateway: %d %s", w.Code, w.Body.String())
				}
			}
		})
	}
}

func TestGatewayAppliesSupplierModelMappings(t *testing.T) {
	s := testApp(t)
	cookie := loginTestApp(t, s)
	var gotBody []byte
	var gotHeader string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotHeader = r.Header.Get("X-Grok-Model-Override")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	sup, ok := s.AIProviders().Supplier("deepseek")
	if !ok {
		t.Fatal("missing deepseek")
	}
	raw, _ := json.Marshal(map[string]any{
		"name":    sup.Name,
		"models":  sup.Models,
		"model_mappings": []map[string]string{
			{"from": "client-*", "to": "up-*"},
			{"from": "legacy", "to": "modern"},
		},
	})
	out := appRequest(s, "PUT", "/api/ai-catalog/deepseek", string(raw), cookie)
	if out.Code != 200 {
		t.Fatalf("save mappings: %d %s", out.Code, out.Body.String())
	}
	var saved aiprovider.Supplier
	if err := json.Unmarshal(out.Body.Bytes(), &saved); err != nil || len(saved.ModelMappings) != 2 {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}

	provider := addRoutingProvider(t, s, cookie, "mapped", "api", map[string]any{
		"supplier": "deepseek", "api_key": "test-key", "api_endpoint": upstream.URL,
	})
	key := routingKey(t, s, cookie, provider)
	body := `{"model":"client-sonnet","metadata":{"model":"keep"},"input":"hi"}`
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+key)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("User-Agent", "grok-cli/1.0")
	r.Header.Set("X-Grok-Model-Override", "client-fast")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("gateway: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(string(gotBody), `"model":"up-sonnet"`) {
		t.Fatalf("body not mapped: %s", gotBody)
	}
	if !strings.Contains(string(gotBody), `"model":"keep"`) {
		t.Fatalf("nested model changed: %s", gotBody)
	}
	if gotHeader != "up-fast" {
		t.Fatalf("header=%q", gotHeader)
	}
}
