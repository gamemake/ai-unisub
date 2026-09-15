package service

import (
	"ai-unisub/internal/aiprovider"
	"ai-unisub/internal/database"
	"encoding/json"
	"testing"
)

func TestStoredCatalogCannotOverrideBuiltinURLs(t *testing.T) {
	for _, fields := range []map[string]any{
		{"url": "https://legacy.invalid/v1"},
		{"claude_url": "https://changed.invalid/claude", "codex_url": "https://changed.invalid/codex"},
	} {
		db := database.NewMemoryDatabase()
		var suppliers []map[string]any
		for _, builtin := range aiprovider.SupplierConfigs() {
			item := map[string]any{"id": builtin.ID, "name": builtin.Name}
			for key, value := range fields {
				item[key] = value
			}
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
