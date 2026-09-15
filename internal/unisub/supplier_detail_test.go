package unisub

import (
	"ai-unisub/internal/aiprovider"
	"encoding/json"
	"testing"
)

func TestSupplierDetailUpdatesAreIsolated(t *testing.T) {
	s := testApp(t)
	cookie := loginTestApp(t, s)
	initial := s.AIProviders().Catalog()
	for _, index := range []int{0, 5} {
		supplier := initial.Suppliers[index]
		supplier.Mappings = []aiprovider.ModelMapping{{Client: aiprovider.ClientOpenAI, Model: "custom", Target: supplier.ID}}
		raw, _ := json.Marshal(supplier)
		w := appRequest(s, "PUT", "/api/ai-catalog/"+supplier.ID, string(raw), cookie)
		if w.Code != 200 {
			t.Fatalf("save %s: %d %s", supplier.ID, w.Code, w.Body.String())
		}
		got := appRequest(s, "GET", "/api/ai-catalog/"+supplier.ID, "", cookie)
		var saved aiprovider.Supplier
		if err := json.Unmarshal(got.Body.Bytes(), &saved); err != nil || saved.ClaudeURL != supplier.ClaudeURL || saved.CodexURL != supplier.CodexURL || len(saved.Mappings) != 1 {
			t.Fatalf("detail: %s %v", got.Body.String(), err)
		}
	}
	c := s.AIProviders().Catalog()
	if c.Suppliers[0].Mappings[0].Target != "anthropic" || c.Suppliers[5].Mappings[0].Target != "kimi" || len(c.Suppliers[1].Mappings) != len(initial.Suppliers[1].Mappings) {
		t.Fatal("overwrote another supplier")
	}
	raw, _ := json.Marshal(initial.Suppliers[0])
	if w := appRequest(s, "PUT", "/api/ai-catalog/kimi", string(raw), cookie); w.Code != 400 {
		t.Fatal("accepted mismatched ID", w.Code)
	}
	if w := appRequest(s, "GET", "/api/ai-catalog/unknown", "", cookie); w.Code != 404 {
		t.Fatal("unknown detail", w.Code)
	}
	stored, err := s.Database().LoadModuleConfig(aiprovider.ModuleConfigKey)
	var persisted aiprovider.Catalog
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(stored, &persisted); err != nil || persisted.Suppliers[0].Mappings[0].Target != c.Suppliers[0].Mappings[0].Target || persisted.Suppliers[5].Mappings[0].Target != c.Suppliers[5].Mappings[0].Target {
		t.Fatal("isolated updates not persisted", err)
	}
}

func TestBuiltinSupplierURLsCannotBeChanged(t *testing.T) {
	s := testApp(t)
	admin := loginTestApp(t, s)
	for _, field := range []string{"claude_url", "codex_url"} {
		for _, path := range []string{"/api/ai-catalog", "/api/ai-catalog/kimi"} {
			c := s.AIProviders().Catalog()
			if field == "claude_url" {
				c.Suppliers[5].ClaudeURL = "https://changed.invalid/v1"
			} else {
				c.Suppliers[5].CodexURL = ""
			}
			var body any = c
			if path != "/api/ai-catalog" {
				body = c.Suppliers[5]
			}
			raw, _ := json.Marshal(body)
			out := appRequest(s, "PUT", path, string(raw), admin)
			if out.Code != 400 {
				t.Fatalf("%s %s: %d %s", path, field, out.Code, out.Body.String())
			}
			if got := s.AIProviders().Catalog().Suppliers[5]; got.ClaudeURL != aiprovider.SupplierConfigs()[5].ClaudeURL || got.CodexURL != aiprovider.SupplierConfigs()[5].CodexURL {
				t.Fatal("modified protected URL")
			}
		}
	}
}
