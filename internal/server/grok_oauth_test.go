package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
	"github.com/ai-unisub/ai-unisub/internal/repository"
)

func TestGrokDeviceOAuthCreatesBoundSubscription(t *testing.T) {
	var tokenPolls atomic.Int32
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		switch r.URL.Path {
		case "/oauth2/device/code":
			if r.Form.Get("client_id") != "test-grok-client" || !strings.Contains(r.Form.Get("scope"), "grok-cli:access") {
				t.Errorf("device form = %v", r.Form)
			}
			if r.Header.Get("x-grok-client-surface") != "ui" {
				t.Errorf("client surface = %q", r.Header.Get("x-grok-client-surface"))
			}
			writeJSON(t, w, http.StatusOK, map[string]any{
				"device_code": "device-secret", "user_code": "ABCD-EFGH",
				"verification_uri": oauthURL(r) + "/activate", "expires_in": 600, "interval": 1,
			})
		case "/oauth2/token":
			if r.Form.Get("grant_type") != grokDeviceGrantType || r.Form.Get("device_code") != "device-secret" {
				t.Errorf("token form = %v", r.Form)
			}
			if tokenPolls.Add(1) == 1 {
				writeJSON(t, w, http.StatusBadRequest, map[string]any{"error": "authorization_pending"})
				return
			}
			writeJSON(t, w, http.StatusOK, map[string]any{
				"access_token": "grok-access", "refresh_token": "grok-refresh", "id_token": "grok-id",
				"token_type": "Bearer", "scope": "openid grok-cli:access", "expires_in": 3600,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer oauth.Close()

	application, repo := testServer(t, oauth.URL+"/responses")
	application.cfg.GrokOAuth.Issuer = oauth.URL
	application.cfg.GrokOAuth.ClientID = "test-grok-client"
	application.cfg.GrokOAuth.Scopes = []string{"openid", "offline_access", "grok-cli:access"}
	adminToken, err := application.signer.issue("admin", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	startBody := `{"name":"grok-subscription","concurrency_limit":2,"concurrency_queue_timeout_seconds":5}`
	start := httptest.NewRequest(http.MethodPost, "/api/providers/grok/oauth/device/start", strings.NewReader(startBody))
	start.Header.Set("Authorization", "Bearer "+adminToken)
	start.Header.Set("Content-Type", "application/json")
	startRecorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(startRecorder, start)
	if startRecorder.Code != http.StatusCreated {
		t.Fatalf("start status=%d body=%s", startRecorder.Code, startRecorder.Body.String())
	}
	var started struct {
		FlowID   string `json:"flow_id"`
		UserCode string `json:"user_code"`
	}
	if err := json.Unmarshal(startRecorder.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	if started.FlowID == "" || started.UserCode != "ABCD-EFGH" || strings.Contains(startRecorder.Body.String(), "device-secret") {
		t.Fatalf("unsafe or incomplete start response: %s", startRecorder.Body.String())
	}

	application.grokOAuthMu.Lock()
	application.grokOAuthFlows[started.FlowID].NextPollAt = time.Now().Add(-time.Second)
	application.grokOAuthMu.Unlock()
	pending := pollOAuth(t, application, adminToken, started.FlowID)
	if pending.Code != http.StatusAccepted || !strings.Contains(pending.Body.String(), "pending") {
		t.Fatalf("pending status=%d body=%s", pending.Code, pending.Body.String())
	}

	application.grokOAuthMu.Lock()
	application.grokOAuthFlows[started.FlowID].NextPollAt = time.Now().Add(-time.Second)
	application.grokOAuthMu.Unlock()
	complete := pollOAuth(t, application, adminToken, started.FlowID)
	if complete.Code != http.StatusCreated {
		t.Fatalf("complete status=%d body=%s", complete.Code, complete.Body.String())
	}
	var completed struct {
		Status  string `json:"status"`
		Subscription struct {
			ID int64 `json:"id"`
		} `json:"subscription"`
	}
	if err := json.Unmarshal(complete.Body.Bytes(), &completed); err != nil {
		t.Fatal(err)
	}
	if completed.Status != "complete" || completed.Subscription.ID == 0 {
		t.Fatalf("complete body = %s", complete.Body.String())
	}
	if strings.Contains(complete.Body.String(), `"api_key"`) {
		t.Fatal("account creation still minted an API key")
	}
	accounts, err := repo.ListSubscriptions(context.Background())
	if err != nil || len(accounts) != 1 {
		t.Fatalf("accounts=%+v err=%v", accounts, err)
	}
	credentials, err := repo.Credentials(context.Background(), accounts[0])
	if err != nil {
		t.Fatal(err)
	}
	if credentials.AccessToken != "grok-access" || credentials.RefreshToken != "grok-refresh" || credentials.ClientID != "test-grok-client" {
		t.Fatalf("credentials = %+v", credentials)
	}
	if accounts[0].TokenExpiresAt == nil || time.Until(*accounts[0].TokenExpiresAt) < 50*time.Minute {
		t.Fatalf("token expiry = %v", accounts[0].TokenExpiresAt)
	}
}

func TestExpiredGrokOAuthTokenRefreshesBeforeProxy(t *testing.T) {
	var refreshed atomic.Bool
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth2/token" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "old-refresh" {
			t.Errorf("refresh form = %v", r.Form)
		}
		refreshed.Store(true)
		writeJSON(t, w, http.StatusOK, map[string]any{
			"access_token": "new-access", "refresh_token": "new-refresh", "token_type": "Bearer", "expires_in": 3600,
		})
	}))
	defer oauth.Close()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer new-access" {
			t.Errorf("upstream authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"response-1"}`)
	}))
	defer upstream.Close()

	application, repo := testServer(t, upstream.URL+"/responses")
	application.cfg.GrokOAuth.Issuer = oauth.URL
	application.cfg.GrokOAuth.ClientID = "test-grok-client"
	expired := time.Now().Add(-time.Minute)
	_, key := createSubscriptionWithKey(t, repo, repository.CreateSubscriptionParams{
		Name: "expired-grok", Provider: model.ProviderGrok, AuthType: "oauth",
		Credentials:    model.Credentials{AccessToken: "old-access", RefreshToken: "old-refresh", ClientID: "test-grok-client"},
		TokenExpiresAt: &expired,
	})

	request := httptest.NewRequest(http.MethodPost, "/grok/v1/responses", bytes.NewBufferString(`{"model":"grok-build","input":"hello"}`))
	request.Header.Set("Authorization", "Bearer "+key)
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !refreshed.Load() {
		t.Fatal("refresh endpoint was not called")
	}
	accounts, err := repo.ListSubscriptions(context.Background())
	if err != nil || len(accounts) != 1 {
		t.Fatalf("accounts=%+v err=%v", accounts, err)
	}
	credentials, err := repo.Credentials(context.Background(), accounts[0])
	if err != nil {
		t.Fatal(err)
	}
	if credentials.AccessToken != "new-access" || credentials.RefreshToken != "new-refresh" {
		t.Fatalf("refreshed credentials = %+v", credentials)
	}
}

func TestGrokCLIProxyHeadersMatchOAuthClientContract(t *testing.T) {
	header := http.Header{}
	injectProviderHeaders(header, model.ProviderGrok, "oauth", model.Credentials{AccessToken: "token"}, "https://cli-chat-proxy.grok.com/v1/responses", "0.2.114", routeResponses)
	want := map[string]string{
		"Authorization":           "Bearer token",
		"User-Agent":              "xai-grok-workspace/0.2.114",
		"X-Grok-Client-Version":   "0.2.114",
		"x-grok-client-identifier": "grok-shell",
		"X-Grok-Client-Mode":      "interactive",
		"X-XAI-Token-Auth":        "xai-grok-cli",
	}
	for name, value := range want {
		if got := header.Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
	if got := header.Get("x-authenticateresponse"); got != "" {
		t.Errorf("unexpected x-authenticateresponse = %q", got)
	}
}

func TestGrokOAuthRetriesOnceAfterUpstreamUnauthorized(t *testing.T) {
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{"access_token": "rotated-access", "expires_in": 3600})
	}))
	defer oauth.Close()
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") == "Bearer stale-access" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Authorization") != "Bearer rotated-access" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(w, `{"id":"response-2"}`)
	}))
	defer upstream.Close()

	application, repo := testServer(t, upstream.URL+"/responses")
	application.cfg.GrokOAuth.Issuer = oauth.URL
	application.cfg.GrokOAuth.ClientID = "test-grok-client"
	_, key := createSubscriptionWithKey(t, repo, repository.CreateSubscriptionParams{
		Name: "grok-without-expiry", Provider: model.ProviderGrok, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "stale-access", RefreshToken: "refresh-token", ClientID: "test-grok-client"},
	})
	request := httptest.NewRequest(http.MethodPost, "/grok/v1/responses", strings.NewReader(`{"model":"grok-build","input":"hello"}`))
	request.Header.Set("Authorization", "Bearer "+key)
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || calls.Load() != 2 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, calls.Load(), recorder.Body.String())
	}
}

func pollOAuth(t *testing.T, application *Server, adminToken, flowID string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"flow_id": flowID})
	request := httptest.NewRequest(http.MethodPost, "/api/providers/grok/oauth/device/poll", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+adminToken)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	return recorder
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(err)
	}
}

func oauthURL(r *http.Request) string {
	return (&url.URL{Scheme: "http", Host: r.Host}).String()
}
