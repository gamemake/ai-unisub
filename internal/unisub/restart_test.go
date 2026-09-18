package unisub

import (
	"ai-unisub/internal/aiprovider"
	"ai-unisub/internal/service"
	"encoding/json/v2"
	"path/filepath"
	"reflect"
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

func TestRestartRestoresAIProviderQuota(t *testing.T) {
	cfg := Config{Service: service.Config{DatabaseURL: "sqlite://" + filepath.ToSlash(filepath.Join(t.TempDir(), "quota.db")), AdminUsername: "admin", AdminPassword: "test-password-123"}}
	first, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cookie := loginTestApp(t, first)
	created := appRequest(first, "POST", "/api/ai-providers", `{"name":"quota","provider":"dummy","config":{"enabled":true}}`, cookie)
	if created.Code != 201 {
		t.Fatalf("create: %s", created.Body.String())
	}
	var account struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &account); err != nil || account.ID == 0 {
		t.Fatalf("create payload: %s", created.Body.String())
	}
	refreshed := appRequest(first, "POST", "/api/ai-providers/"+pathID(account.ID)+"/refresh-quota", "", cookie)
	if refreshed.Code != 200 {
		t.Fatalf("refresh: %s", refreshed.Body.String())
	}
	var want aiprovider.Quota
	if err := json.Unmarshal(refreshed.Body.Bytes(), &want); err != nil || len(want.Subscription) != 2 {
		t.Fatalf("refresh payload: %s", refreshed.Body.String())
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	cookie = loginTestApp(t, second)
	listed := appRequest(second, "GET", "/api/ai-providers", "", cookie)
	var result struct {
		Items []struct {
			ID    int              `json:"id"`
			Quota aiprovider.Quota `json:"quota"`
		} `json:"items"`
	}
	if listed.Code != 200 || json.Unmarshal(listed.Body.Bytes(), &result) != nil {
		t.Fatalf("list: %d %s", listed.Code, listed.Body.String())
	}
	for _, item := range result.Items {
		if item.ID != account.ID {
			continue
		}
		if len(item.Quota.Subscription) != 2 || !reflect.DeepEqual(item.Quota.Subscription, want.Subscription) {
			t.Fatalf("restored quota %+v; want %+v", item.Quota, want)
		}
		return
	}
	t.Fatal("restored provider missing quota")
}
