package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
	"github.com/ai-unisub/ai-unisub/internal/repository"
)

func TestRefreshGrokUsageFetchesAndNormalizesCredits(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/user" {
			if r.URL.Query().Get("include") != "subscription" {
				t.Errorf("user query = %s", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `{"userId":"user-123","email":"user@example.com","subscriptionTier":"SuperGrok Heavy"}`)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/v1/billing" || r.URL.Query().Get("format") != "credits" {
			t.Errorf("billing request = %s %s", r.Method, r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer grok-access-token" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("X-XAI-Token-Auth"); got != "xai-grok-cli" {
			t.Errorf("token auth header = %q", got)
		}
		if got := r.Header.Get("x-userid"); got != "user-123" {
			t.Errorf("user id header = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"subscriptionTier": "SuperGrok Heavy",
			"config": {
				"creditUsagePercent": "37.5%",
				"currentPeriod": {
					"type": "USAGE_PERIOD_TYPE_WEEKLY",
					"start": "2026-08-17T00:00:00Z",
					"end": "2026-08-24T00:00:00Z"
				},
				"prepaidBalance": {"val": 7},
				"onDemandCap": {"val": 25},
				"onDemandUsed": {"val": 13}
			}
		}`)
	}))
	defer upstream.Close()

	application, repo := testServer(t, upstream.URL+"/responses")
	application.cfg.Providers.GrokBilling = upstream.URL + "/v1/billing?format=credits"
	account, err := repo.CreateAccount(context.Background(), repository.CreateAccountParams{
		Name: "grok-usage", Provider: model.ProviderGrok, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "grok-access-token", RefreshToken: "refresh-token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	adminToken, err := application.signer.issue("admin", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/accounts/"+strconv.FormatInt(account.ID, 10)+"/usage/refresh", nil)
	request.Header.Set("Authorization", "Bearer "+adminToken)
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	var response struct {
		Status           string `json:"status"`
		SubscriptionTier string `json:"subscription_tier"`
		Windows          []struct {
			Name             string  `json:"name"`
			UsedPercent      float64 `json:"used_percent"`
			RemainingPercent float64 `json:"remaining_percent"`
			ResetAt          string  `json:"reset_at"`
		} `json:"windows"`
		CreditBalance map[string]any `json:"credit_balance"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "available" || response.SubscriptionTier != "SuperGrok Heavy" {
		t.Fatalf("usage response = %+v", response)
	}
	if len(response.Windows) != 1 || response.Windows[0].Name != "weekly" || response.Windows[0].UsedPercent != 37.5 || response.Windows[0].RemainingPercent != 62.5 || response.Windows[0].ResetAt == "" {
		t.Fatalf("usage windows = %+v", response.Windows)
	}
	if response.CreditBalance["prepaid"] == nil || response.CreditBalance["on_demand"] == nil {
		t.Fatalf("credit balance = %+v", response.CreditBalance)
	}
	if response.CreditBalance["prepaid"].(float64) != 7 || response.CreditBalance["on_demand"].(map[string]any)["remaining"].(float64) != 12 {
		t.Fatalf("normalized credit balance = %+v", response.CreditBalance)
	}

	stored, err := repo.GetAccount(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.QuotaCheckedAt == nil || stored.QuotaError != nil || len(stored.Quota) == 0 {
		t.Fatalf("stored quota checked=%v error=%v quota=%s", stored.QuotaCheckedAt, stored.QuotaError, stored.Quota)
	}
	credentials, err := repo.Credentials(context.Background(), stored)
	if err != nil || credentials.UserID != "user-123" || credentials.Email != "user@example.com" {
		t.Fatalf("stored Grok profile = %+v err=%v", credentials, err)
	}
}

func TestNormalizeGrokBillingFallsBackToLegacyMonthlyFields(t *testing.T) {
	body := []byte("{\"subscriptionTier\":\"SuperGrok\",\"config\":{\"monthlyLimit\":{\"val\":2000},\"used\":{\"val\":500},\"billingPeriodStart\":\"2026-08-01T00:00:00Z\",\"billingPeriodEnd\":\"2026-09-01T00:00:00Z\"}}")
	quota, err := normalizeGrokBilling(nil, body)
	if err != nil {
		t.Fatal(err)
	}
	var normalized struct {
		Windows []struct {
			UsedPercent      float64 `json:"used_percent"`
			RemainingPercent float64 `json:"remaining_percent"`
			ResetAt          string  `json:"reset_at"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(quota, &normalized); err != nil {
		t.Fatal(err)
	}
	if len(normalized.Windows) != 1 || normalized.Windows[0].UsedPercent != 25 || normalized.Windows[0].RemainingPercent != 75 || normalized.Windows[0].ResetAt == "" {
		t.Fatalf("legacy monthly quota = %+v", normalized.Windows)
	}
}

func TestRefreshGrokUsagePreservesLastGoodQuotaOnFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "temporary failure", http.StatusServiceUnavailable)
	}))
	defer upstream.Close()

	application, repo := testServer(t, upstream.URL+"/responses")
	application.cfg.Providers.GrokBilling = upstream.URL + "/v1/billing?format=credits"
	account, err := repo.CreateAccount(context.Background(), repository.CreateAccountParams{
		Name: "grok-usage-error", Provider: model.ProviderGrok, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "grok-access-token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	lastGood := json.RawMessage(`{"subscription_tier":"SuperGrok","windows":[{"name":"weekly","remaining_percent":50}]}`)
	if err := repo.UpdateAccountQuota(context.Background(), account.ID, lastGood, time.Now().Add(-time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	account, _ = repo.GetAccount(context.Background(), account.ID)
	if _, err := application.refreshGrokQuota(context.Background(), account); err == nil {
		t.Fatal("billing failure was accepted")
	}
	stored, err := repo.GetAccount(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored.Quota) != string(lastGood) || stored.QuotaError == nil {
		t.Fatalf("last good quota was not preserved: quota=%s error=%v", stored.Quota, stored.QuotaError)
	}
}
