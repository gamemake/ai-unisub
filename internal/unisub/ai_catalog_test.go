package unisub

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"ai-unisub/internal/database"
	framework "ai-unisub/internal/service"
)

func TestSuppliersMatchFrontendShape(t *testing.T) {
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

	type supplier struct {
		ID                      string              `json:"id"`
		Models                  []string            `json:"models"`
		ModelMappings           []map[string]string `json:"model_mappings"`
		SupportedClients        []string            `json:"supported_clients"`
		SubscriptionPlanWeights map[string]int      `json:"subscription_plan_weights"`
	}
	var response struct {
		Suppliers        []supplier `json:"suppliers"`
		BuiltinSuppliers []supplier `json:"builtin_suppliers"`
	}
	listed := call(http.MethodGet, "/api/suppliers", nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", listed.Code, listed.Body.String())
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Suppliers) == 0 || len(response.BuiltinSuppliers) != len(response.Suppliers) {
		t.Fatalf("catalog = %s", listed.Body.String())
	}
	var anthropic supplier
	for _, item := range response.Suppliers {
		if item.ID == "anthropic" {
			anthropic = item
		}
	}
	if len(anthropic.SupportedClients) != 1 || anthropic.SupportedClients[0] != "claude" || anthropic.SubscriptionPlanWeights["claude_max_5x"] != 5 {
		t.Fatalf("anthropic = %+v", anthropic)
	}

	updated := call(http.MethodPut, "/api/suppliers/anthropic", map[string]any{
		"id": "anthropic", "models": anthropic.Models,
		"model_mappings":            []map[string]string{{"from": "claude-*", "to": anthropic.Models[0]}},
		"subscription_plan_weights": map[string]int{"claude_pro": 1, "claude_max_5x": 6, "claude_max_20x": 20},
	})
	if updated.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", updated.Code, updated.Body.String())
	}
	var result supplier
	if err := json.Unmarshal(updated.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.ModelMappings) != 1 || result.ModelMappings[0]["from"] != "claude-*" || result.SubscriptionPlanWeights["claude_max_5x"] != 6 {
		t.Fatalf("updated = %s", updated.Body.String())
	}

	created := call(http.MethodPost, "/api/accounts", map[string]any{
		"name": "dummy",
		"config": map[string]any{
			"kind": "subscription", "supplier": "dummy", "subscription_plan": "claude_pro", "enabled": true,
			"credential": map[string]string{"access_token": "dummy-token"},
		},
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", created.Code, created.Body.String())
	}
	var account struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &account); err != nil {
		t.Fatal(err)
	}
	listedModels := call(http.MethodGet, "/api/accounts/"+strconv.Itoa(account.ID)+"/models", nil)
	var models struct {
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.Unmarshal(listedModels.Body.Bytes(), &models); err != nil || listedModels.Code != http.StatusOK || len(models.Models) == 0 || models.Models[0].ID == "" {
		t.Fatalf("models status = %d, body = %s, err = %v", listedModels.Code, listedModels.Body.String(), err)
	}
}
