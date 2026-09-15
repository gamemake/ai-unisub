package unisub

import (
	"ai-unisub/internal/aiprovider"
	"encoding/json"
	"net/http"
	"testing"
)

func TestModelMappingUsesOriginalNamesForEachSupplier(t *testing.T) {
	s := testApp(t)
	for _, tc := range []struct{ ua, model string }{
		{"claude-cli/2.1.220", "claude-sonnet-4-6"},
		{"codex-tui/0.146.0", "gpt-5.6-sol"},
		{"grok-shell/1.0", "grok-build"},
	} {
		t.Run(tc.ua, func(t *testing.T) {
			original := http.Header{"User-Agent": {tc.ua}, "X-Grok-Model-Override": {tc.model}}
			out := original.Clone()
			body := []byte(`{"model":"` + tc.model + `","input":"hello"}`)
			for _, supplier := range []string{"kimi", "deepseek"} {
				result := mapRequestModel(s.AIProviders(), original, out, supplier, body)
				var v map[string]string
				if err := json.Unmarshal(result, &v); err != nil {
					t.Fatal(err)
				}
				want := s.AIProviders().MapModel(aiprovider.DetectClient(original), supplier, tc.model)
				if v["model"] != want || v["input"] != "hello" {
					t.Fatalf("bad body: %s", result)
				}
				if aiprovider.DetectClient(original) == aiprovider.ClientGrok && out.Get("X-Grok-Model-Override") != want {
					t.Fatal("Grok header not remapped")
				}
			}
			if original.Get("X-Grok-Model-Override") != tc.model {
				t.Fatal("original headers mutated")
			}
		})
	}
}

func TestClientTypeSavedAsScalar(t *testing.T) {
	s := testApp(t)
	cookie := loginTestApp(t, s)
	id := addRoutingProvider(t, s, cookie, "scalar", "dummy", map[string]any{"client_type": "OpenAI", "client_types": []string{"Anthropic", "Grok"}})
	accounts, err := s.Database().ListAccounts()
	if err != nil {
		t.Fatal(err)
	}
	for _, account := range accounts {
		if account.ID != id {
			continue
		}
		var config map[string]any
		if err := json.Unmarshal(account.Config, &config); err != nil {
			t.Fatal(err)
		}
		if config["client_type"] != "OpenAI" || config["client_types"] != nil {
			t.Fatalf("not a scalar config: %s", account.Config)
		}
		return
	}
	t.Fatal("saved provider missing")
}
