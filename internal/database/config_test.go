package database

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConfigCRUD(t *testing.T) {
	db := testDB(t)

	all, err := db.ListConfigs()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("expected empty list, got %d", len(all))
	}

	first := &PersistedConfig{
		Type:  "proxy-policy",
		Name:  "default",
		Value: json.RawMessage(`{"mode":"round_robin"}`),
	}
	if err := db.SaveConfig(first); err != nil {
		t.Fatal(err)
	}
	if first.ID <= 0 {
		t.Fatal("expected positive ID after create")
	}

	second := &PersistedConfig{
		Type:  "proxy-policy",
		Name:  "backup",
		Value: json.RawMessage(`{"mode":"failover"}`),
	}
	if err := db.SaveConfig(second); err != nil {
		t.Fatal(err)
	}
	other := &PersistedConfig{
		Type:  "feature-flag",
		Name:  "default",
		Value: json.RawMessage(`{"enabled":true}`),
	}
	if err := db.SaveConfig(other); err != nil {
		t.Fatal(err)
	}

	all, err = db.ListConfigs()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 configs, got %d", len(all))
	}
	if all[0].ID > all[1].ID || all[1].ID > all[2].ID {
		t.Fatalf("expected configs ordered by ID: %+v", all)
	}

	byType, err := db.ListConfigsByType("proxy-policy")
	if err != nil {
		t.Fatal(err)
	}
	if len(byType) != 2 {
		t.Fatalf("expected 2 proxy-policy configs, got %d", len(byType))
	}

	loaded, err := db.LoadConfig("proxy-policy", "default")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != first.ID || loaded.Type != first.Type || loaded.Name != first.Name {
		t.Fatalf("unexpected loaded config: %+v", loaded)
	}
	if string(loaded.Value) != `{"mode":"round_robin"}` {
		t.Fatalf("unexpected value: %s", loaded.Value)
	}

	missing, err := db.LoadConfig("proxy-policy", "missing")
	if err != nil {
		t.Fatal(err)
	}
	if missing.ID != 0 {
		t.Fatalf("expected zero-value missing config, got %+v", missing)
	}

	first.Value = json.RawMessage(`{"mode":"weighted"}`)
	if err := db.SaveConfig(first); err != nil {
		t.Fatal(err)
	}
	loaded, err = db.LoadConfig("proxy-policy", "default")
	if err != nil {
		t.Fatal(err)
	}
	if string(loaded.Value) != `{"mode":"weighted"}` {
		t.Fatalf("expected updated value, got %s", loaded.Value)
	}

	if err := db.DeleteConfig(first.ID); err != nil {
		t.Fatal(err)
	}
	loaded, err = db.LoadConfig("proxy-policy", "default")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != 0 {
		t.Fatalf("expected deleted config to be missing, got %+v", loaded)
	}
	all, err = db.ListConfigs()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 configs after delete, got %d", len(all))
	}
}

func TestConfigUpdateRejectsTypeOrNameChange(t *testing.T) {
	db := testDB(t)
	cfg := &PersistedConfig{
		Type:  "feature-flag",
		Name:  "beta",
		Value: json.RawMessage(`{"enabled":false}`),
	}
	if err := db.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	cfg.Type = "other-type"
	if err := db.SaveConfig(cfg); err == nil {
		t.Fatal("expected type change to fail")
	}
	cfg.Type = "feature-flag"
	cfg.Name = "renamed"
	if err := db.SaveConfig(cfg); err == nil {
		t.Fatal("expected name change to fail")
	}

	loaded, err := db.LoadConfig("feature-flag", "beta")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != cfg.ID || string(loaded.Value) != `{"enabled":false}` {
		t.Fatalf("config should be unchanged after rejected updates: %+v", loaded)
	}
}

func TestConfigValidationAndUniqueness(t *testing.T) {
	db := testDB(t)

	if err := db.SaveConfig(&PersistedConfig{Type: "t", Name: "n", Value: json.RawMessage(`not-json`)}); err == nil {
		t.Fatal("invalid JSON was accepted")
	}
	if err := db.SaveConfig(&PersistedConfig{Type: "", Name: "n", Value: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("empty type was accepted")
	}
	if err := db.SaveConfig(&PersistedConfig{Type: " t", Name: "n", Value: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("padded type was accepted")
	}
	if err := db.SaveConfig(&PersistedConfig{Type: "t", Name: " n", Value: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("padded name was accepted")
	}
	if _, err := db.ListConfigsByType(""); err == nil {
		t.Fatal("empty type list was accepted")
	}
	if _, err := db.LoadConfig("", "n"); err == nil {
		t.Fatal("empty type load was accepted")
	}

	cfg := &PersistedConfig{Type: "t", Name: "n", Value: json.RawMessage(`{"a":1}`)}
	if err := db.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	dup := &PersistedConfig{Type: "t", Name: "n", Value: json.RawMessage(`{"a":2}`)}
	if err := db.SaveConfig(dup); err == nil {
		t.Fatal("duplicate type+name was accepted")
	} else if !strings.Contains(strings.ToLower(err.Error()), "unique") && !strings.Contains(strings.ToLower(err.Error()), "constraint") {
		// SQLite wording varies; ensure it still failed.
		t.Logf("duplicate create failed with: %v", err)
	}

	missing := &PersistedConfig{ID: 999, Type: "t", Name: "missing", Value: json.RawMessage(`{}`)}
	if err := db.SaveConfig(missing); err == nil {
		t.Fatal("update of missing id was accepted")
	}
}

func TestConfigCacheSurvivesReload(t *testing.T) {
	path := t.TempDir() + "/configs.db"
	db := NewSQLiteDatabase(path)
	if err := db.Open(); err != nil {
		t.Fatal(err)
	}
	cfg := &PersistedConfig{
		Type:  "feature-flag",
		Name:  "rollout",
		Value: json.RawMessage(`{"percent":25}`),
	}
	if err := db.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	id := cfg.ID
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db2 := NewSQLiteDatabase(path)
	if err := db2.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db2.Close() })

	loaded, err := db2.LoadConfig("feature-flag", "rollout")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != id || string(loaded.Value) != `{"percent":25}` {
		t.Fatalf("expected reloaded config, got %+v", loaded)
	}
	all, err := db2.ListConfigs()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 config after reload, got %d", len(all))
	}
}
