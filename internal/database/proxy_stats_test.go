package database

import (
	"ai-unisub/internal/proxy"
	"path/filepath"
	"testing"
	"time"
)

func TestProxyStatsIdempotentAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.db")
	db := NewSQLiteDatabase(path)
	if err := db.Open(); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(10 * time.Minute)
	b := proxy.Bucket{Address: "http://localhost:8080", Application: "app", StartAt: at, Source: "process-one", Requests: 3, Failures: 1}
	for range 2 {
		if err := db.SaveProxyStats([]proxy.Bucket{b}); err != nil {
			t.Fatal(err)
		}
	}
	disabled := false
	g := PersistedProxyGroup{ID: "disabled", Name: "Disabled", Enabled: &disabled, CreatedAt: at, UpdatedAt: at}
	if err := db.SaveProxyGroup(&g); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = NewSQLiteDatabase(path)
	if err := db.Open(); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	b.Source = "process-two"
	b.Requests = 2
	b.Failures = 0
	if err := db.SaveProxyStats([]proxy.Bucket{b}); err != nil {
		t.Fatal(err)
	}
	rows, err := db.ListProxyStats(b.Address, b.Application, at, at.Add(10*time.Minute))
	if err != nil || len(rows) != 1 || rows[0].Requests != 5 || rows[0].Failures != 1 {
		t.Fatalf("history %+v: %v", rows, err)
	}
	groups, err := db.ListProxyGroups()
	if err != nil || len(groups) != 1 || groups[0].Enabled == nil || *groups[0].Enabled {
		t.Fatal("group enable flag was not persisted")
	}
}
