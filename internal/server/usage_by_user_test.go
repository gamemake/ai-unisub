package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
	"github.com/ai-unisub/ai-unisub/internal/repository"
)

func TestAdminCanQueryUsageByUser(t *testing.T) {
	application, repo := testServer(t, "https://example.invalid/responses")
	ctx := context.Background()
	user, err := repo.CreateUser(ctx, "member", "member-password-ok", model.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	subscription, err := repo.CreateSubscription(ctx, repository.CreateSubscriptionParams{
		Name: "shared", Provider: model.ProviderCodex, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	otherSubscription, err := repo.CreateSubscription(ctx, repository.CreateSubscriptionParams{
		Name: "other", Provider: model.ProviderClaude, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "other-token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := repo.CreateAPIKey(ctx, repository.CreateAPIKeyParams{SubscriptionID: subscription.ID, UserID: &user.ID, Name: "member-key"})
	if err != nil {
		t.Fatal(err)
	}
	otherKey, _, err := repo.CreateAPIKey(ctx, repository.CreateAPIKeyParams{SubscriptionID: otherSubscription.ID, UserID: &user.ID, Name: "other-key"})
	if err != nil {
		t.Fatal(err)
	}
	input, output, cacheCreate, cacheRead := int64(12), int64(5), int64(3), int64(7)
	started := time.Date(2026, 9, 7, 9, 0, 0, 0, time.Local)
	if err := repo.RecordRequest(time.Local, repository.RequestLog{
		SubscriptionID: &subscription.ID, APIKeyID: &key.ID, UserID: &user.ID,
		Provider: "codex", Method: "POST", Path: "/v1/responses", StatusCode: 200,
		StartedAt: started, FinishedAt: started.Add(time.Second), InputTokens: &input, OutputTokens: &output,
		CacheCreationTokens: &cacheCreate, CacheReadTokens: &cacheRead,
	}); err != nil {
		t.Fatal(err)
	}
	otherInput, otherOutput := int64(100), int64(50)
	if err := repo.RecordRequest(time.Local, repository.RequestLog{
		SubscriptionID: &otherSubscription.ID, APIKeyID: &otherKey.ID, UserID: &user.ID,
		Provider: "claude", Method: "POST", Path: "/v1/messages", StatusCode: 200,
		StartedAt: started.Add(time.Minute), FinishedAt: started.Add(time.Minute + time.Second), InputTokens: &otherInput, OutputTokens: &otherOutput,
	}); err != nil {
		t.Fatal(err)
	}
	adminToken, err := application.signer.issue("admin", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/usage/by-user?from=2026-09-07&to=2026-09-07&subscription_id=%d", subscription.ID), nil)
	request.Header.Set("Authorization", "Bearer "+adminToken)
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Totals model.UsageTotals    `json:"totals"`
		Data   []model.UserUsageRow `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Totals.Requests != 1 || response.Totals.TotalTokens != 17 || response.Totals.CacheCreationTokens != 3 || response.Totals.CacheReadTokens != 7 {
		t.Fatalf("totals=%+v", response.Totals)
	}
	if len(response.Data) != 1 {
		t.Fatalf("zero-usage users should be excluded: %+v", response.Data)
	}
	var found bool
	for _, row := range response.Data {
		if row.UserID != nil && *row.UserID == user.ID {
			found = row.Username == "member" && row.Usage.Requests == 1 && row.Usage.TotalTokens == 17 && row.Usage.CacheCreationTokens == 3 && row.Usage.CacheReadTokens == 7
		}
	}
	if !found {
		t.Fatalf("member usage not found: %+v", response.Data)
	}
}

func TestUsageByUserRejectsInvalidSubscriptionFilter(t *testing.T) {
	application, _ := testServer(t, "https://example.invalid/responses")
	adminToken, err := application.signer.issue("admin", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/usage/by-user?range=1d&subscription_id=invalid", nil)
	request.Header.Set("Authorization", "Bearer "+adminToken)
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMemberCannotQueryUsageByUser(t *testing.T) {
	application, repo := testServer(t, "https://example.invalid/responses")
	if _, err := repo.CreateUser(context.Background(), "member", "member-password-ok", model.RoleUser); err != nil {
		t.Fatal(err)
	}
	token, err := application.signer.issue("member", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/usage/by-user?range=1d", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
