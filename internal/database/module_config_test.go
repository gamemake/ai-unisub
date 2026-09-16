package database

import (
	"ai-unisub/internal/proxy"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestModuleConfigIsolation(t *testing.T) {
	for name, db := range map[string]Database{"memory": NewMemoryDatabase(), "sqlite": NewSQLiteDatabase(":memory:")} {
		t.Run(name, func(t *testing.T) {
			if err := db.Open(); err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			input := json.RawMessage(`{"enabled":true}`)
			for _, module := range []string{"aiprovider", "proxy"} {
				if err := db.SaveModuleConfig(module, input); err != nil {
					t.Fatal(err)
				}
			}
			input[0] = '['
			got, err := db.LoadModuleConfig("proxy")
			if err != nil || string(got) != `{"enabled":true}` {
				t.Fatalf("input alias: %s, %v", got, err)
			}
			got[0] = '['
			got, err = db.LoadModuleConfig("proxy")
			if err != nil || string(got) != `{"enabled":true}` {
				t.Fatalf("output alias: %s, %v", got, err)
			}
			if err := db.SaveModuleConfig("aiprovider", json.RawMessage(`{"version":2}`)); err != nil {
				t.Fatal(err)
			}
			if err := db.SaveModuleConfig("proxy", json.RawMessage(`invalid`)); err == nil {
				t.Fatal("accepted invalid JSON")
			}
			if err := db.SaveModuleConfig(" ", json.RawMessage(`{}`)); err == nil {
				t.Fatal("accepted blank module")
			}
			if _, err := db.LoadModuleConfig(""); err == nil {
				t.Fatal("accepted empty module")
			}
			if err := db.DeleteModuleConfig(""); err == nil {
				t.Fatal("accepted empty module deletion")
			}
			for range 2 {
				if err := db.DeleteModuleConfig("aiprovider"); err != nil {
					t.Fatal(err)
				}
			}
			if got, err := db.LoadModuleConfig("aiprovider"); err != nil || got != nil {
				t.Fatalf("deleted config: %s, %v", got, err)
			}
			if got, err := db.LoadModuleConfig("proxy"); err != nil || string(got) != `{"enabled":true}` {
				t.Fatalf("module isolation: %s, %v", got, err)
			}
		})
	}
}

func TestModuleConfigAndProxyHealthPersistAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db := NewSQLiteDatabase(path)
	if err := db.Open(); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"setting":"value"}`)
	if err := db.SaveModuleConfig("example", raw); err != nil {
		t.Fatal(err)
	}
	record := proxy.HealthRecord{Address: "http://localhost:8001", Application: "claude", State: proxy.State{Status: "unavailable", Failures: 2, LastError: proxy.ApplicationError}, UpdatedAt: time.Now().UTC()}
	for range 2 {
		if err := db.SaveProxyHealth([]proxy.HealthRecord{record}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = NewSQLiteDatabase(path)
	if err := db.Open(); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := db.LoadModuleConfig("example")
	if err != nil || string(got) != string(raw) {
		t.Fatal("catalog lost", err)
	}
	var count int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM proxy_health`).Scan(&count); err != nil || count != 1 {
		t.Fatal("health duplicated", count, err)
	}
	manager := proxy.NewManager(db, proxy.DefaultPolicy())
	defer manager.Close()
	e, _ := proxy.NewEndpoint(record.Address)
	if manager.Snapshot(e, "claude").Status != "unknown" {
		t.Fatal("historical health seeded scheduling")
	}
}
