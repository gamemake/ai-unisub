package unisub

import (
	"ai-unisub/internal/aiprovider"
	"ai-unisub/internal/database"
	"encoding/json"
	"testing"
)

func TestSupplierDetailUpdatesAreIsolated(t *testing.T) {
	s := testApp(t)
	cookie := loginTestApp(t, s)
	initial := s.AIProviders().Catalog()
	for _, index := range []int{0, 5} {
		supplier := initial.Suppliers[index]
		body := map[string]any{
			"id":     supplier.ID,
			"name":   supplier.ID + " custom",
			"models": supplier.Models,
		}
		raw, _ := json.Marshal(body)
		w := appRequest(s, "PUT", "/api/ai-catalog/"+supplier.ID, string(raw), cookie)
		if w.Code != 200 {
			t.Fatalf("save %s: %d %s", supplier.ID, w.Code, w.Body.String())
		}
		got := appRequest(s, "GET", "/api/ai-catalog/"+supplier.ID, "", cookie)
		var saved aiprovider.Supplier
		if err := json.Unmarshal(got.Body.Bytes(), &saved); err != nil || saved.ClaudeURL != supplier.ClaudeURL || saved.OpenAIURL != supplier.OpenAIURL || saved.Name != supplier.ID+" custom" {
			t.Fatalf("detail: %s %v", got.Body.String(), err)
		}
	}
	c := s.AIProviders().Catalog()
	if c.Suppliers[0].Name != "anthropic custom" || c.Suppliers[5].Name != "kimi custom" || c.Suppliers[1].Name != initial.Suppliers[1].Name {
		t.Fatal("overwrote another supplier")
	}
	raw, _ := json.Marshal(map[string]any{"id": "anthropic", "name": "x", "models": []string{}})
	if w := appRequest(s, "PUT", "/api/ai-catalog/kimi", string(raw), cookie); w.Code != 400 {
		t.Fatal("accepted mismatched ID", w.Code, w.Body.String())
	}
	if w := appRequest(s, "GET", "/api/ai-catalog/unknown", "", cookie); w.Code != 404 {
		t.Fatal("unknown detail", w.Code)
	}
	cfg, err := s.Database().LoadConfig(aiprovider.SupplierConfigType, "anthropic")
	if err != nil || cfg.ID == 0 {
		t.Fatal("anthropic overlay not persisted", err, cfg)
	}
	cfgKimi, err := s.Database().LoadConfig(aiprovider.SupplierConfigType, "kimi")
	if err != nil || cfgKimi.ID == 0 {
		t.Fatal("kimi overlay not persisted", err)
	}
	cfgOpenAI, err := s.Database().LoadConfig(aiprovider.SupplierConfigType, "openai")
	if err != nil {
		t.Fatal(err)
	}
	if cfgOpenAI.ID != 0 {
		t.Fatal("openai should not have overlay")
	}
}

func TestBuiltinSupplierURLsCannotBeChanged(t *testing.T) {
	s := testApp(t)
	admin := loginTestApp(t, s)
	for _, field := range []string{"claude_url", "openai_url", "codex_url"} {
		body := map[string]any{
			"id":     "kimi",
			"name":   "Kimi",
			"models": aiprovider.SupplierConfigs()[5].Models,
			field:   "https://changed.invalid/v1",
		}
		raw, _ := json.Marshal(body)
		out := appRequest(s, "PUT", "/api/ai-catalog/kimi", string(raw), admin)
		if out.Code != 400 {
			t.Fatalf("%s: %d %s", field, out.Code, out.Body.String())
		}
		if got := s.AIProviders().Catalog().Suppliers[5]; got.ClaudeURL != aiprovider.SupplierConfigs()[5].ClaudeURL || got.OpenAIURL != aiprovider.SupplierConfigs()[5].OpenAIURL {
			t.Fatal("modified protected URL")
		}
	}
	if w := appRequest(s, "PUT", "/api/ai-catalog", `{"suppliers":[]}`, admin); w.Code != 405 {
		t.Fatal("batch put should be rejected", w.Code)
	}
}

func TestSupplierResetToBuiltinDeletesOverlay(t *testing.T) {
	s := testApp(t)
	admin := loginTestApp(t, s)
	builtin := aiprovider.SupplierConfigs()[0]
	raw, _ := json.Marshal(map[string]any{"id": "anthropic", "name": "Custom", "models": []string{"custom-model"}})
	if w := appRequest(s, "PUT", "/api/ai-catalog/anthropic", string(raw), admin); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	raw, _ = json.Marshal(map[string]any{"id": "anthropic", "name": builtin.Name, "models": builtin.Models})
	if w := appRequest(s, "PUT", "/api/ai-catalog/anthropic", string(raw), admin); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	cfg, err := s.Database().LoadConfig(aiprovider.SupplierConfigType, "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ID != 0 {
		t.Fatalf("expected overlay deleted, got %+v", cfg)
	}
	// Ensure PersistedConfig list is empty for supplier type after full reset.
	rows, err := s.Database().ListConfigsByType(aiprovider.SupplierConfigType)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Name == "anthropic" {
			t.Fatal("anthropic row still present")
		}
	}
	_ = database.PersistedConfig{}
}
