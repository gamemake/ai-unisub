package aiprovider

import (
	"ai-unisub/internal/database"
	"ai-unisub/internal/oauth"
	"ai-unisub/internal/proxy"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// Dummy accounts need no OAuth credential and never contact an upstream.
func TestDummySupplierServesLocally(t *testing.T) {
	db, err := database.NewDatabase("sqlite::memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Open(); err != nil {
		t.Fatal(err)
	}
	proxyManager := proxy.NewManager(db)
	if err := proxyManager.Open(); err != nil {
		t.Fatal(err)
	}
	provider, err := NewProviderManager(db, proxyManager, oauth.NewManager(db, proxyManager))
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = provider.Close()
		_ = proxyManager.Close()
		_ = db.Close()
	})

	config, err := json.Marshal(AccountConfig{
		Kind: AccountSubscription, Name: "dummy", Supplier: "dummy", Enabled: true, MaxConcurrentConnections: 1,
		SubscriptionPlan: "claude_pro",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := provider.NewAccount(config)
	if err != nil {
		t.Fatal(err)
	}

	quota, err := provider.FetchQuota(t.Context(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(quota.Subscription) != 2 || quota.CacheStatus != QuotaCacheFresh {
		t.Fatalf("quota = %+v", quota)
	}
	want := []string{"dummy-fast", "dummy-long-context", "dummy-model", "dummy-reasoning"}
	if got, err := provider.GetModels(t.Context(), account.ID, ""); err != nil || !slices.Equal(got, want) {
		t.Fatalf("GetModels() = %v, %v", got, err)
	}

	now := time.Now().UTC()
	user := &database.PersistedUser{Name: "dummy-user", Role: database.UserRoleUser, Enabled: true, PasswordHash: "unused", CreatedAt: now, UpdatedAt: now}
	if err := db.SaveUser(user); err != nil {
		t.Fatal(err)
	}
	key := &database.PersistedAPIKey{Name: "dummy-key", UserID: user.ID, AccountID: account.ID, Key: "client-secret", CreatedAt: now, UpdatedAt: now}
	if err := db.SaveAPIKey(key); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"dummy-model","messages":[]}`))
	req.Header.Set("X-Api-Key", "client-secret")
	result := httptest.NewRecorder()
	provider.Handler().ServeHTTP(result, req)
	if result.Code != http.StatusOK || result.Body.String() != dummyResponseBody {
		t.Fatalf("gateway status = %d, body = %s", result.Code, result.Body.String())
	}
}
