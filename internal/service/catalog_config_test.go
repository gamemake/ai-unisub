package service

import (
	"ai-unisub/internal/aiprovider"
	"ai-unisub/internal/database"
	"encoding/json"
	"maps"
	"testing"
)

func TestStoredCatalogCannotOverrideBuiltinURLs(t *testing.T) {
	for _, fields := range []map[string]any{
		{"url": "https://legacy.invalid/v1"},
		{"claude_url": "https://changed.invalid/claude", "codex_url": "https://changed.invalid/codex"},
		{"openai_url": "https://changed.invalid/openai"},
	} {
		db, err := database.NewDatabase("sqlite::memory:")
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Open(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		var suppliers []map[string]any
		for _, builtin := range aiprovider.SupplierConfigs() {
			item := map[string]any{"id": builtin.ID, "name": builtin.Name}
			maps.Copy(item, fields)
			suppliers = append(suppliers, item)
		}
		raw, _ := json.Marshal(map[string]any{"suppliers": suppliers})
		if err := db.SaveModuleConfig(aiprovider.ModuleConfigKey, raw); err != nil {
			t.Fatal(err)
		}
		s, err := NewWithDependencies(Config{DatabaseURL: "sqlite::memory:"}, db, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, builtin := range aiprovider.SupplierConfigs() {
			for _, client := range []aiprovider.ClientType{aiprovider.ClientClaude, aiprovider.ClientCodex, aiprovider.ClientGrok} {
				if got := s.AIProviders().DefaultURL(builtin.ID, client); got != builtin.URLForClient(client) {
					t.Fatal(builtin.ID, client, got)
				}
			}
		}
		s.Close()
	}
}

func TestSupplierOverlayPersistsAcrossRestart(t *testing.T) {
	db, err := database.NewDatabase("sqlite::memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s, err := NewWithDependencies(Config{DatabaseURL: "sqlite::memory:"}, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AIProviders().UpdateSupplier("deepseek", aiprovider.SupplierConfigurable{
		Name:   "DeepSeek Custom",
		Models: []string{"deepseek-chat"},
	}); err != nil {
		t.Fatal(err)
	}
	// Reload overlays onto a fresh manager without closing the shared DB.
	p2 := aiprovider.NewAIProviderManager()
	if err := loadSupplierCatalog(db, p2); err != nil {
		t.Fatal(err)
	}
	got, ok := p2.Supplier("deepseek")
	if !ok || got.Name != "DeepSeek Custom" || len(got.Models) != 1 || got.Models[0] != "deepseek-chat" {
		t.Fatalf("overlay not reloaded: %+v", got)
	}
	if got.OpenAIURL != aiprovider.SupplierConfigs()[3].OpenAIURL {
		t.Fatal("URL should remain builtin")
	}
	_ = s
}
