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

func TestRefreshCodexUsageFetchesWindows(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/backend-api/wham/usage" {
			t.Errorf("usage request = %s %s", r.Method, r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer codex-access-token" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("ChatGPT-Account-Id"); got != "acct-123" {
			t.Errorf("account id = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"plan_type": "plus",
			"rate_limit": {
				"primary_window": {
					"limit_window_seconds": 18000,
					"used_percent": 42.5,
					"reset_at": 1789000000
				},
				"secondary_window": {
					"limit_window_seconds": 604800,
					"used_percent": 18,
					"reset_at": 1789600000
				}
			},
			"credits": {"balance": 3.25}
		}`)
	}))
	defer upstream.Close()

	application, repo := testServer(t, upstream.URL+"/responses")
	application.cfg.Providers.CodexUsage = upstream.URL + "/backend-api/wham/usage"
	account, err := repo.CreateSubscription(context.Background(), repository.CreateSubscriptionParams{
		Name: "codex-usage", Provider: model.ProviderCodex,
		Credentials: model.Credentials{AccessToken: "codex-access-token", RefreshToken: "refresh-token", ChatGPTAccountID: "acct-123"},
	})
	if err != nil {
		t.Fatal(err)
	}
	adminToken, err := application.signer.issue("admin", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/subscriptions/"+strconv.FormatInt(account.ID, 10)+"/usage/refresh", nil)
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
	if response.Status != "available" || response.SubscriptionTier != "plus" {
		t.Fatalf("usage response = %+v", response)
	}
	if len(response.Windows) != 2 || response.Windows[0].Name != "five_hour" || response.Windows[0].UsedPercent != 42.5 {
		t.Fatalf("primary window = %+v", response.Windows)
	}
	if response.Windows[1].Name != "seven_day" || response.Windows[1].UsedPercent != 18 || response.Windows[1].RemainingPercent != 82 {
		t.Fatalf("secondary window = %+v", response.Windows[1])
	}
	if response.CreditBalance["balance"].(float64) != 3.25 {
		t.Fatalf("credit balance = %+v", response.CreditBalance)
	}
}

func TestCodexWindowNameUsesDuration(t *testing.T) {
	if got := codexWindowName(18000, "", 0, 0); got != "five_hour" {
		t.Fatalf("5h name = %s", got)
	}
	if got := codexWindowName(604800, "secondary", 0, 0); got != "seven_day" {
		t.Fatalf("7d name = %s", got)
	}
	if got := codexWindowName(86400, "secondary", 1_000_000+3*24*3600, 1_000_000); got != "seven_day" {
		t.Fatalf("24h weekly cadence name = %s", got)
	}
}
