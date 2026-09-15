package unisub

import (
	"ai-unisub/internal/service"
	"path/filepath"
	"testing"
)

func TestRestartRestoresAIProviderRuntimeAndPreservesAdminPassword(t *testing.T) {
	cfg := Config{Service: service.Config{DatabaseURL: "sqlite://" + filepath.ToSlash(filepath.Join(t.TempDir(), "app.db")), AdminUsername: "admin", AdminPassword: "test-password-123"}}
	first, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cookie := loginTestApp(t, first)
	created := appRequest(first, "POST", "/api/ai-providers", `{"name":"saved","provider":"dummy","config":{"enabled":true}}`, cookie)
	if created.Code != 201 {
		t.Fatalf("create: %s", created.Body.String())
	}
	accounts, _ := first.Database().ListAccounts()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	cfg.Service.AdminPassword = "changed-bootstrap-password"
	second, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, ok := second.AIProviders().GetAccount(accounts[0].ID); !ok {
		t.Fatal("persisted provider runtime not restored")
	}
	// Existing users keep their password across bootstrap configuration changes.
	loginTestApp(t, second)
}
