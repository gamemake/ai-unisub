package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
	"github.com/ai-unisub/ai-unisub/internal/repository"
)

func TestAccountUsageReturnsStoredQuotaForOverview(t *testing.T) {
	application, repo := testServer(t, "https://example.invalid/responses")
	account, err := repo.CreateAccount(context.Background(), repository.CreateAccountParams{
		Name: "quota-account", Provider: model.ProviderGrok, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	quota := json.RawMessage(`{
		"subscription_tier":"supergrok",
		"windows":[{"name":"five_hour","used_percent":32.5,"remaining_percent":67.5,"reset_at":"2026-08-20T14:00:00Z"}],
		"request_quota":{"remaining":80,"limit":100},
		"token_quota":{"remaining":9000,"limit":10000}
	}`)
	if err := repo.UpdateAccountQuota(context.Background(), account.ID, quota, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	adminToken, err := application.signer.issue("admin", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/accounts/1/usage", nil)
	request.Header.Set("Authorization", "Bearer "+adminToken)
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Status           string `json:"status"`
		Source           string `json:"source"`
		SubscriptionTier string `json:"subscription_tier"`
		Windows          []struct {
			Name             string  `json:"name"`
			RemainingPercent float64 `json:"remaining_percent"`
		} `json:"windows"`
		RequestQuota map[string]float64 `json:"request_quota"`
		TokenQuota   map[string]float64 `json:"token_quota"`
		Stale        bool               `json:"stale"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "available" || response.Source != "upstream" || response.SubscriptionTier != "supergrok" {
		t.Fatalf("quota response = %+v", response)
	}
	if len(response.Windows) != 1 || response.Windows[0].RemainingPercent != 67.5 {
		t.Fatalf("windows = %+v", response.Windows)
	}
	if response.RequestQuota["remaining"] != 80 || response.TokenQuota["remaining"] != 9000 || response.Stale {
		t.Fatalf("quota details = %+v", response)
	}
}

func TestAccountUsageKeepsUnknownWhenQuotaUnavailable(t *testing.T) {
	application, repo := testServer(t, "https://example.invalid/responses")
	account, err := repo.CreateAccount(context.Background(), repository.CreateAccountParams{
		Name: "unknown-quota", Provider: model.ProviderClaude, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	message := "upstream usage endpoint is unavailable"
	if err := repo.UpdateAccountQuota(context.Background(), account.ID, nil, time.Now(), &message); err != nil {
		t.Fatal(err)
	}
	adminToken, _ := application.signer.issue("admin", time.Hour)
	request := httptest.NewRequest(http.MethodGet, "/api/accounts/1/usage", nil)
	request.Header.Set("Authorization", "Bearer "+adminToken)
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"status":"unknown"`) || !strings.Contains(recorder.Body.String(), message) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
