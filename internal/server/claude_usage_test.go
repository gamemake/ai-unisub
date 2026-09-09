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

func TestRefreshClaudeUsageFetchesWindows(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/oauth/usage" {
			t.Errorf("usage request = %s %s", r.Method, r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer claude-access-token" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("anthropic-beta"); got != "oauth-2025-04-20" {
			t.Errorf("anthropic-beta = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"five_hour": {"utilization": 33.0, "resets_at": "2026-09-07T12:00:00.000Z"},
			"seven_day": {"utilization": 13.5, "resets_at": "2026-09-14T00:00:00.000Z"},
			"seven_day_opus": null,
			"seven_day_sonnet": {"utilization": 1.0, "resets_at": "2026-09-10T03:00:00.000Z"},
			"extra_usage": {"is_enabled": true, "monthly_limit": 100, "used_credits": 12.5}
		}`)
	}))
	defer upstream.Close()

	application, repo := testServer(t, upstream.URL+"/responses")
	application.cfg.Providers.ClaudeUsage = upstream.URL + "/api/oauth/usage"
	account, err := repo.CreateSubscription(context.Background(), repository.CreateSubscriptionParams{
		Name: "claude-usage", Provider: model.ProviderClaude,
		Credentials: model.Credentials{AccessToken: "claude-access-token", RefreshToken: "refresh-token"},
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
		Status  string `json:"status"`
		Windows []struct {
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
	if response.Status != "available" || len(response.Windows) < 2 {
		t.Fatalf("usage response = %+v", response)
	}
	if response.Windows[0].Name != "five_hour" || response.Windows[0].UsedPercent != 33 || response.Windows[0].RemainingPercent != 67 {
		t.Fatalf("five_hour window = %+v", response.Windows[0])
	}
	if response.Windows[1].Name != "seven_day" || response.Windows[1].UsedPercent != 13.5 {
		t.Fatalf("seven_day window = %+v", response.Windows[1])
	}
	if response.CreditBalance["monthly_limit"].(float64) != 100 || response.CreditBalance["used_credits"].(float64) != 12.5 {
		t.Fatalf("credit balance = %+v", response.CreditBalance)
	}
}

func TestNormalizeClaudeUsageRejectsEmptyPayload(t *testing.T) {
	if _, err := normalizeClaudeUsage(nil, []byte(`{"five_hour":null,"seven_day":null}`)); err == nil {
		t.Fatal("empty Claude usage was accepted")
	}
}
