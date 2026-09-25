package aiprovider2

import (
	"ai-unisub/internal/database"
	"ai-unisub/internal/oauth2"
	"ai-unisub/internal/proxy2"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAccountAccessUsesScheduledGroupMember(t *testing.T) {
	manager := &providerManager{
		accounts:     make(map[int]*Account),
		suppliersMap: make(map[string]Supplier),
	}
	manager.suppliersMap["deepseek"] = newSupplierDeepSeek(manager)
	member := &Account{
		manager: manager,
		ID:      1,
		Config: AccountConfig{
			Kind:     AccountAPI,
			Supplier: "deepseek",
			APIKey:   "member-key",
		},
	}
	manager.accounts[member.ID] = member
	group := &Account{
		manager: manager,
		ID:      2,
		Config: AccountConfig{
			Kind:    AccountGroup,
			Members: []GroupMember{{ID: member.ID, Weight: 1}},
		},
	}
	newGroupScheduler(group)
	req := httptest.NewRequest("POST", "/v1/messages", nil)

	token, baseURL, err := group.GetAccess(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if token != "member-key" {
		t.Fatalf("GetAccess() token = %q, want member key", token)
	}
	if baseURL != "https://api.deepseek.com/anthropic/v1" {
		t.Fatalf("GetAccess() base URL = %q, want Claude-compatible URL", baseURL)
	}
}

func TestAccountGetAccessLoadsOAuthCredential(t *testing.T) {
	manager := &providerManager{suppliersMap: make(map[string]Supplier)}
	manager.suppliersMap["openai"] = newSupplierOpenAI(manager)
	account := &Account{
		manager: manager,
		State:   AccountState{RefreshAt: time.Now().UTC()},
		Config: AccountConfig{
			Kind:       AccountOAuth,
			Supplier:   "openai",
			Credential: oauth2.OAuthCredential{AccessToken: "oauth-token"},
		},
	}

	token, baseURL, err := account.GetAccess(t.Context(), httptest.NewRequest("POST", "/v1/responses", nil))
	if err != nil {
		t.Fatal(err)
	}
	if token != "oauth-token" {
		t.Fatalf("GetAccess() token = %q, want OAuth token", token)
	}
	if baseURL != "https://chatgpt.com/backend-api/codex" {
		t.Fatalf("GetAccess() base URL = %q, want Codex OAuth URL", baseURL)
	}
}

func TestAccountGetAccessRefreshesStaleOAuthCredential(t *testing.T) {
	db, err := database.NewDatabase("sqlite::memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Open(); err != nil {
		t.Fatal(err)
	}
	proxyManager := proxy2.NewManager(db)
	if err := proxyManager.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := proxyManager.Close(); err != nil {
			t.Error(err)
		}
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	credential := oauth2.OAuthCredential{
		AccessToken:  "oauth-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    time.Now().Add(24 * time.Hour),
	}
	manager := &providerManager{db: db, oauth: oauth2.NewManager(db, proxyManager)}
	previousRefresh := time.Now().Add(-18 * time.Hour)
	account := &Account{
		manager: manager,
		State:   AccountState{RefreshAt: previousRefresh},
		Config: AccountConfig{
			Kind:        AccountOAuth,
			Supplier:    oauth2.OAuthServiceDummy,
			Credential:  credential,
			APIEndpoint: "https://gateway.example/v1",
		},
	}

	token, _, err := account.GetAccess(t.Context(), httptest.NewRequest("POST", "/v1/responses", nil))
	if err != nil {
		t.Fatal(err)
	}
	if token != credential.AccessToken {
		t.Fatalf("GetAccess() token = %q, want refreshed token %q", token, credential.AccessToken)
	}
	if !account.State.RefreshAt.After(previousRefresh) || !account.Dirty {
		t.Fatalf("refresh state was not updated: state=%+v dirty=%v", account.State, account.Dirty)
	}
	if account.Config.Credential.AccessToken != credential.AccessToken {
		t.Fatalf("refreshed credential was not stored in account config: %+v", account.Config.Credential)
	}
}

func TestAccountGetAccessUsesCustomEndpoint(t *testing.T) {
	account := &Account{Config: AccountConfig{
		Kind:        AccountAPI,
		APIKey:      "api-key",
		APIEndpoint: "https://gateway.example/v1",
	}}

	token, baseURL, err := account.GetAccess(t.Context(), httptest.NewRequest("POST", "/v1/responses", nil))
	if err != nil {
		t.Fatal(err)
	}
	if token != "api-key" {
		t.Fatalf("GetAccess() token = %q, want API key", token)
	}
	if baseURL != "https://gateway.example/v1" {
		t.Fatalf("GetAccess() base URL = %q, want custom endpoint", baseURL)
	}
}

func TestAccountUpdateConfigPreservesCredentialWithSameRefreshToken(t *testing.T) {
	manager := &providerManager{suppliersMap: make(map[string]Supplier)}
	manager.suppliersMap["openai"] = newSupplierOpenAI(manager)
	account := &Account{
		manager: manager,
		Config: AccountConfig{
			Kind:     AccountOAuth,
			Name:     "old name",
			Supplier: "openai",
			Credential: oauth2.OAuthCredential{
				AccessToken:  "current-access-token",
				RefreshToken: "shared-refresh-token",
			},
		},
	}

	err := account.UpdateConfig(json.RawMessage(`{
		"kind":"oauth",
		"name":"new name",
		"supplier":"openai",
		"credential":{
			"refresh_token":"shared-refresh-token"
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if account.Config.Name != "new name" {
		t.Fatalf("account name = %q, want updated name", account.Config.Name)
	}
	if account.Config.Credential.AccessToken != "current-access-token" {
		t.Fatalf("credential was overwritten: %+v", account.Config.Credential)
	}
}

func TestAccessTokenNeedsRefresh(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name      string
		refreshAt time.Time
		expiresAt time.Time
		want      bool
	}{
		{name: "fresh", refreshAt: now.Add(-time.Hour), expiresAt: now.Add(time.Hour)},
		{name: "never refreshed", expiresAt: now.Add(time.Hour), want: true},
		{name: "refresh interval elapsed", refreshAt: now.Add(-17 * time.Hour), expiresAt: now.Add(time.Hour), want: true},
		{name: "expires soon", refreshAt: now.Add(-time.Hour), expiresAt: now.Add(4 * time.Minute), want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := accessTokenNeedsRefresh(now, test.refreshAt, test.expiresAt); got != test.want {
				t.Fatalf("accessTokenNeedsRefresh() = %v, want %v", got, test.want)
			}
		})
	}
}
