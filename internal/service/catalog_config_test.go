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
			for _, client := range []aiprovider.ClientType{aiprovider.ClientAnthropic, aiprovider.ClientOpenAI, aiprovider.ClientGrok} {
				if got := s.AIProviders().DefaultURL(builtin.ID, client); got != builtin.URLForClient(client) {
					t.Fatal(builtin.ID, client, got)
				}
			}

		}
		s.Close()
	}
}
