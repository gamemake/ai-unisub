package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
	"github.com/ai-unisub/ai-unisub/internal/repository"
)

func TestUpdateSubscriptionAPIEditsSettingsWithoutEchoingProxy(t *testing.T) {
	application, repo := testServer(t, "https://example.invalid/responses")
	account, err := repo.CreateSubscription(context.Background(), repository.CreateSubscriptionParams{
		Name: "grok-edit", Provider: model.ProviderGrok, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "token"},
		ConcurrencyLimit: 1, ProxyURL: "http://127.0.0.1:8080",
	})
	if err != nil {
		t.Fatal(err)
	}
	adminToken, err := application.signer.issue("admin", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	body := `{"name":"grok-edited","enabled":false,"concurrency_limit":3,"concurrency_queue_timeout_seconds":12,"proxy_url":"http://127.0.0.1:9090"}`
	request := httptest.NewRequest(http.MethodPut, "/api/subscriptions/"+strconv.FormatInt(account.ID, 10), bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer "+adminToken)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Subscription model.Subscription `json:"subscription"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Subscription.Name != "grok-edited" || response.Subscription.Enabled || response.Subscription.ConcurrencyLimit != 3 {
		t.Fatalf("account = %+v", response.Subscription)
	}
	if response.Subscription.ConcurrencyQueueTimeoutSeconds != 12 {
		t.Fatalf("limits = %+v", response.Subscription)
	}
	if !response.Subscription.ProxyConfigured || stringsContainsProxySecret(recorder.Body.Bytes()) {
		t.Fatalf("proxy response = %s", recorder.Body.String())
	}

	stored, err := repo.GetSubscription(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ProxyURL != "http://127.0.0.1:9090" {
		t.Fatalf("stored proxy = %q", stored.ProxyURL)
	}
}

func TestUpdateSubscriptionAPIKeepsProxyWhenOmitted(t *testing.T) {
	application, repo := testServer(t, "https://example.invalid/responses")
	account, err := repo.CreateSubscription(context.Background(), repository.CreateSubscriptionParams{
		Name: "keep-proxy", Provider: model.ProviderClaude, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "token"},
		ProxyURL:    "socks5://127.0.0.1:1080",
	})
	if err != nil {
		t.Fatal(err)
	}
	adminToken, err := application.signer.issue("admin", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"name":"keep-proxy","enabled":true,"concurrency_limit":1,"concurrency_queue_timeout_seconds":0}`
	request := httptest.NewRequest(http.MethodPut, "/api/subscriptions/"+strconv.FormatInt(account.ID, 10), bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer "+adminToken)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	stored, err := repo.GetSubscription(context.Background(), account.ID)
	if err != nil || stored.ProxyURL != "socks5://127.0.0.1:1080" {
		t.Fatalf("stored proxy = %+v err=%v", stored, err)
	}
}

func stringsContainsProxySecret(body []byte) bool {
	return bytes.Contains(body, []byte("127.0.0.1:9090")) || bytes.Contains(body, []byte("proxy_url"))
}

func TestGetSubscriptionReturnsCredentialsJSON(t *testing.T) {
	application, repo := testServer(t, "https://example.invalid/responses")
	account, err := repo.CreateSubscription(context.Background(), repository.CreateSubscriptionParams{
		Name: "show-creds", Provider: model.ProviderGrok, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "secret-access", RefreshToken: "secret-refresh"},
	})
	if err != nil {
		t.Fatal(err)
	}
	adminToken, err := application.signer.issue("admin", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	list := httptest.NewRequest(http.MethodGet, "/api/subscriptions", nil)
	list.Header.Set("Authorization", "Bearer "+adminToken)
	listRecorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(listRecorder, list)
	if listRecorder.Code != http.StatusOK || bytes.Contains(listRecorder.Body.Bytes(), []byte("secret-access")) {
		t.Fatalf("list leaked credentials: status=%d body=%s", listRecorder.Code, listRecorder.Body.String())
	}

	request := httptest.NewRequest(http.MethodGet, "/api/subscriptions/"+strconv.FormatInt(account.ID, 10), nil)
	request.Header.Set("Authorization", "Bearer "+adminToken)
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Subscription struct {
			Credentials map[string]string `json:"credentials"`
		} `json:"subscription"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Subscription.Credentials["access_token"] != "secret-access" || response.Subscription.Credentials["refresh_token"] != "secret-refresh" {
		t.Fatalf("credentials = %+v", response.Subscription.Credentials)
	}
}

func TestUpdateSubscriptionReplacesCredentialsJSON(t *testing.T) {
	application, repo := testServer(t, "https://example.invalid/responses")
	account, err := repo.CreateSubscription(context.Background(), repository.CreateSubscriptionParams{
		Name: "edit-creds", Provider: model.ProviderClaude, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "old-token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	adminToken, err := application.signer.issue("admin", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"name":"edit-creds","enabled":true,"concurrency_limit":1,"concurrency_queue_timeout_seconds":0,"credentials":{"access_token":"new-token","refresh_token":"new-refresh"}}`
	request := httptest.NewRequest(http.MethodPut, "/api/subscriptions/"+strconv.FormatInt(account.ID, 10), bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer "+adminToken)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	stored, err := repo.GetSubscription(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := repo.Credentials(context.Background(), stored)
	if err != nil || credentials.AccessToken != "new-token" || credentials.RefreshToken != "new-refresh" {
		t.Fatalf("stored credentials = %+v err=%v", credentials, err)
	}
}
