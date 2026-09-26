package unisub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	aiprovider "ai-unisub/internal/aiprovider"
	"ai-unisub/internal/database"
	framework "ai-unisub/internal/service"
)

func TestGatewayModuleUsesVersionTwoProviderHandler(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer upstream-secret" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	defer upstream.Close()

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
	if err := service.AddModule(NewGatewayModule()); err != nil {
		t.Fatal(err)
	}

	config, err := json.Marshal(aiprovider.AccountConfig{
		Kind: aiprovider.AccountAPI, Name: "test", Supplier: "openai",
		ClientType: aiprovider.ClientCodex, APIEndpoint: upstream.URL + "/v1",
		APIKey: "upstream-secret", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := service.AIProviders().NewAccount(config)
	if err != nil {
		t.Fatal(err)
	}
	user := &database.PersistedUser{Name: "user", Role: database.UserRoleUser, Enabled: true}
	if err := db.SaveUser(user); err != nil {
		t.Fatal(err)
	}
	key := &database.PersistedAPIKey{Name: "key", UserID: user.ID, AccountID: account.ID, Key: "client-secret"}
	if err := db.SaveAPIKey(key); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set("Authorization", "Bearer client-secret")
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}
