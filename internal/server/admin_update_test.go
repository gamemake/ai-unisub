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

func TestUpdateAccountAPIEditsSettingsWithoutEchoingProxy(t *testing.T) {
	application, repo := testServer(t, "https://example.invalid/responses")
	account, err := repo.CreateAccount(context.Background(), repository.CreateAccountParams{
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
	request := httptest.NewRequest(http.MethodPut, "/admin/accounts/"+strconv.FormatInt(account.ID, 10), bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer "+adminToken)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Account model.Account `json:"account"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Account.Name != "grok-edited" || response.Account.Enabled || response.Account.ConcurrencyLimit != 3 {
		t.Fatalf("account = %+v", response.Account)
	}
	if response.Account.ConcurrencyQueueTimeoutSeconds != 12 {
		t.Fatalf("limits = %+v", response.Account)
	}
	if !response.Account.ProxyConfigured || stringsContainsProxySecret(recorder.Body.Bytes()) {
		t.Fatalf("proxy response = %s", recorder.Body.String())
	}

	stored, err := repo.GetAccount(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ProxyURL != "http://127.0.0.1:9090" {
		t.Fatalf("stored proxy = %q", stored.ProxyURL)
	}
}

func TestUpdateAccountAPIKeepsProxyWhenOmitted(t *testing.T) {
	application, repo := testServer(t, "https://example.invalid/responses")
	account, err := repo.CreateAccount(context.Background(), repository.CreateAccountParams{
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
	request := httptest.NewRequest(http.MethodPut, "/admin/accounts/"+strconv.FormatInt(account.ID, 10), bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer "+adminToken)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	stored, err := repo.GetAccount(context.Background(), account.ID)
	if err != nil || stored.ProxyURL != "socks5://127.0.0.1:1080" {
		t.Fatalf("stored proxy = %+v err=%v", stored, err)
	}
}

func stringsContainsProxySecret(body []byte) bool {
	return bytes.Contains(body, []byte("127.0.0.1:9090")) || bytes.Contains(body, []byte("proxy_url"))
}

func TestGetAccountReturnsCredentialsJSON(t *testing.T) {
	application, repo := testServer(t, "https://example.invalid/responses")
	account, err := repo.CreateAccount(context.Background(), repository.CreateAccountParams{
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

	list := httptest.NewRequest(http.MethodGet, "/admin/accounts", nil)
	list.Header.Set("Authorization", "Bearer "+adminToken)
	listRecorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(listRecorder, list)
	if listRecorder.Code != http.StatusOK || bytes.Contains(listRecorder.Body.Bytes(), []byte("secret-access")) {
		t.Fatalf("list leaked credentials: status=%d body=%s", listRecorder.Code, listRecorder.Body.String())
	}

	request := httptest.NewRequest(http.MethodGet, "/admin/accounts/"+strconv.FormatInt(account.ID, 10), nil)
	request.Header.Set("Authorization", "Bearer "+adminToken)
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Account struct {
			Credentials map[string]string `json:"credentials"`
		} `json:"account"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Account.Credentials["access_token"] != "secret-access" || response.Account.Credentials["refresh_token"] != "secret-refresh" {
		t.Fatalf("credentials = %+v", response.Account.Credentials)
	}
}

func TestUpdateAccountReplacesCredentialsJSON(t *testing.T) {
	application, repo := testServer(t, "https://example.invalid/responses")
	account, err := repo.CreateAccount(context.Background(), repository.CreateAccountParams{
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
	request := httptest.NewRequest(http.MethodPut, "/admin/accounts/"+strconv.FormatInt(account.ID, 10), bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer "+adminToken)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	stored, err := repo.GetAccount(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := repo.Credentials(context.Background(), stored)
	if err != nil || credentials.AccessToken != "new-token" || credentials.RefreshToken != "new-refresh" {
		t.Fatalf("stored credentials = %+v err=%v", credentials, err)
	}
}
