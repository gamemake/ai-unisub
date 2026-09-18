package proxy

import (
	"ai-unisub/internal/database"
	"testing"
	"time"
)

func TestStoreProxyForDBNewPersists(t *testing.T) {
	db, err := database.NewDatabase("sqlite::memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	policy := DefaultPolicy()
	policy.AutoProbe = false
	m := NewManager(NewStoreProxyForDB(db), policy)
	t.Cleanup(func() { _ = m.Close() })
	now := time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)
	g := Group{Name: "web", Remark: "from ui", MaxRetries: 1, CreatedAt: now, UpdatedAt: now, Proxies: []Entry{{URL: "socks5h://127.0.0.1:1080", Enabled: true}}}
	if err := m.New(&g); err != nil {
		t.Fatal(err)
	}
	if g.ID <= 0 || g.Proxies[0].ID == "" {
		t.Fatalf("New must assign IDs: %+v", g)
	}
	listed, err := m.List()
	if err != nil || len(listed) != 1 || listed[0].ID != g.ID || listed[0].Name != "web" || listed[0].Remark != "from ui" {
		t.Fatalf("created group not loaded: %+v %v", listed, err)
	}
	g.Remark = "saved"
	if err := m.Save(&g); err != nil {
		t.Fatal(err)
	}
	listed, err = m.List()
	if err != nil || len(listed) != 1 || listed[0].Remark != "saved" {
		t.Fatalf("updated group not loaded: %+v %v", listed, err)
	}
}
