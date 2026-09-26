package unisub

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	aiprovider "ai-unisub/internal/aiprovider"
	"ai-unisub/internal/database"
	framework "ai-unisub/internal/service"
)

func TestSubscriptionAccountCreateListAndEdit(t *testing.T) {
	db, err := database.NewDatabase("sqlite::memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Open(); err != nil {
		t.Fatal(err)
	}
	service, err := framework.NewWithDependencies(framework.Config{}, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()
	if err := service.AddModule(NewAPIModule()); err != nil {
		t.Fatal(err)
	}
	if err := service.Auth().EnsureAdmin("admin", "admin-password"); err != nil {
		t.Fatal(err)
	}

	var cookies []*http.Cookie
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		request := httptest.NewRequest(method, path, bytes.NewReader(raw))
		request.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		service.Handler().ServeHTTP(response, request)
		return response
	}
	login := call(http.MethodPost, "/api/login", map[string]string{"username": "admin", "password": "admin-password"})
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", login.Code, login.Body.String())
	}
	cookies = login.Result().Cookies()

	created := call(http.MethodPost, "/api/accounts", map[string]any{
		"name": "codex-main",
		"config": map[string]any{
			"kind": "subscription", "supplier": "openai", "subscription_plan": "codex_plus", "enabled": true,
			"credential": map[string]string{"access_token": "access-secret", "refresh_token": "refresh-secret"},
		},
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", created.Code, created.Body.String())
	}
	var item struct {
		ID     int                      `json:"id"`
		Config aiprovider.AccountConfig `json:"config"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	if item.Config.Kind != aiprovider.AccountSubscription || item.Config.Supplier != "openai" {
		t.Fatalf("created account = %+v", item)
	}
	if item.Config.Credential.AccessToken != "" || item.Config.Credential.RefreshToken != "" {
		t.Fatalf("response leaked credential: %+v", item.Config.Credential)
	}
	legacyProvider := call(http.MethodPost, "/api/accounts", map[string]any{
		"name": "legacy", "provider": "openai",
		"config": map[string]any{"kind": "subscription", "supplier": "openai"},
	})
	if legacyProvider.Code != http.StatusBadRequest {
		t.Fatalf("legacy provider status = %d, want 400; body = %s", legacyProvider.Code, legacyProvider.Body.String())
	}

	// Edits without a new credential keep the stored one.
	updated := call(http.MethodPut, "/api/accounts/"+strconv.Itoa(item.ID), map[string]any{
		"name":   "codex-renamed",
		"config": map[string]any{"kind": "subscription", "supplier": "openai", "subscription_plan": "codex_pro_5x", "enabled": true},
	})
	if updated.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", updated.Code, updated.Body.String())
	}
	account := service.AIProviders().ListAccounts()[0]
	if account.Config.Name != "codex-renamed" || account.Config.SubscriptionPlan != "codex_pro_5x" {
		t.Fatalf("config not updated: %+v", account.Config)
	}
	if account.Config.Credential.AccessToken != "access-secret" {
		t.Fatalf("stored credential lost: %+v", account.Config.Credential)
	}

	changedKind := call(http.MethodPut, "/api/accounts/"+strconv.Itoa(item.ID), map[string]any{
		"config": map[string]any{"kind": "api", "supplier": "openai", "api_key": "sk-test"},
	})
	if changedKind.Code != http.StatusBadRequest {
		t.Fatalf("kind change status = %d, want 400", changedKind.Code)
	}
}
