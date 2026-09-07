package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
	"github.com/ai-unisub/ai-unisub/internal/repository"
)

func TestParseOAuthErrorBody(t *testing.T) {
	standard := parseOAuthErrorBody([]byte(`{"error":"invalid_grant","error_description":"Invalid 'code' in request."}`))
	if standard.Error != "invalid_grant" || standard.ErrorDescription == "" {
		t.Fatalf("standard oauth error = %+v", standard)
	}
	nested := parseOAuthErrorBody([]byte(`{"type":"error","error":{"type":"not_found_error","message":"Not found"}}`))
	if nested.Error != "not_found_error" || nested.ErrorDescription != "Not found" {
		t.Fatalf("nested anthropic error = %+v", nested)
	}
}

func TestParseAuthorizationInput(t *testing.T) {
	tests := []struct {
		raw   string
		code  string
		state string
	}{
		{raw: "plain-code", code: "plain-code"},
		{raw: "abc123#state-value", code: "abc123", state: "state-value"},
		{raw: "http://localhost:1455/auth/callback?code=codex-code&state=codex-state", code: "codex-code", state: "codex-state"},
		{raw: "https://console.anthropic.com/oauth/code/callback#code=frag-code&state=frag-state", code: "frag-code", state: "frag-state"},
		{raw: "https://platform.claude.com/oauth/code/callback#code=plat-code&state=plat-state", code: "plat-code", state: "plat-state"},
	}
	for _, test := range tests {
		code, state, err := parseAuthorizationInput(test.raw)
		if err != nil {
			t.Fatalf("parse %q: %v", test.raw, err)
		}
		if code != test.code || state != test.state {
			t.Fatalf("parse %q = %q/%q, want %q/%q", test.raw, code, state, test.code, test.state)
		}
	}
	if _, _, err := parseAuthorizationInput(""); err == nil {
		t.Fatal("empty input was accepted")
	}
}

func TestClaudePKCEOAuthCreatesBoundAccount(t *testing.T) {
	var sawJSON atomic.Bool
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/oauth/token" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content type = %q", r.Header.Get("Content-Type"))
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]string
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["grant_type"] != "authorization_code" || payload["code"] != "claude-code" || payload["code_verifier"] == "" || payload["state"] == "" {
			t.Errorf("token payload = %v", payload)
		}
		sawJSON.Store(true)
		writeJSON(t, w, http.StatusOK, map[string]any{
			"access_token": "claude-access", "refresh_token": "claude-refresh",
			"token_type": "Bearer", "expires_in": 3600, "scope": "user:inference",
		})
	}))
	defer oauth.Close()

	application, repo := testServer(t, oauth.URL+"/messages")
	application.cfg.ClaudeOAuth.AuthorizeURL = oauth.URL + "/oauth/authorize"
	application.cfg.ClaudeOAuth.TokenURL = oauth.URL + "/v1/oauth/token"
	application.cfg.ClaudeOAuth.RedirectURI = "https://platform.claude.com/oauth/code/callback"
	application.cfg.ClaudeOAuth.ClientID = "test-claude-client"
	adminToken := adminBearer(t, application)

	started := startPKCE(t, application, adminToken, "claude", `{"name":"claude-subscription"}`)
	if started.FlowID == "" || started.AuthorizationURL == "" || strings.Contains(started.AuthorizationURL, "code_verifier") {
		t.Fatalf("unsafe or incomplete start: %+v", started)
	}
	if !strings.Contains(started.AuthorizationURL, "code=true") || !strings.Contains(started.AuthorizationURL, "client_id=test-claude-client") {
		t.Fatalf("authorization url = %s", started.AuthorizationURL)
	}

	application.pkceOAuthMu.Lock()
	state := application.pkceOAuthFlows[started.FlowID].State
	application.pkceOAuthMu.Unlock()
	complete := exchangePKCE(t, application, adminToken, "claude", started.FlowID, "claude-code#"+state)
	if complete.Code != http.StatusCreated {
		t.Fatalf("exchange status=%d body=%s", complete.Code, complete.Body.String())
	}
	if !sawJSON.Load() {
		t.Fatal("Claude token endpoint was not called")
	}
	accounts, err := repo.ListSubscriptions(context.Background())
	if err != nil || len(accounts) != 1 {
		t.Fatalf("accounts=%+v err=%v", accounts, err)
	}
	credentials, err := repo.Credentials(context.Background(), accounts[0])
	if err != nil {
		t.Fatal(err)
	}
	if credentials.AccessToken != "claude-access" || credentials.RefreshToken != "claude-refresh" || credentials.ClientID != "test-claude-client" {
		t.Fatalf("credentials = %+v", credentials)
	}
	if accounts[0].Provider != model.ProviderClaude || accounts[0].TokenExpiresAt == nil {
		t.Fatalf("account = %+v", accounts[0])
	}
}

func TestCodexPKCEOAuthExtractsSubscriptionIDFromCallbackURL(t *testing.T) {
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "codex-code" || r.Form.Get("code_verifier") == "" {
			t.Errorf("token form = %v", r.Form)
		}
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("content type = %q", r.Header.Get("Content-Type"))
		}
		idToken := testJWT(map[string]any{
			"email": "plus@example.com",
			"https://api.openai.com/auth": map[string]any{
				"chatgpt_account_id": "acct_123",
				"chatgpt_user_id":    "user_456",
				"organization_id":    "org_789",
			},
		})
		writeJSON(t, w, http.StatusOK, map[string]any{
			"access_token": "codex-access", "refresh_token": "codex-refresh", "id_token": idToken,
			"token_type": "Bearer", "expires_in": 3600,
		})
	}))
	defer oauth.Close()

	application, repo := testServer(t, oauth.URL+"/responses")
	application.cfg.CodexOAuth.AuthorizeURL = oauth.URL + "/oauth/authorize"
	application.cfg.CodexOAuth.TokenURL = oauth.URL + "/oauth/token"
	application.cfg.CodexOAuth.RedirectURI = "http://localhost:1455/auth/callback"
	application.cfg.CodexOAuth.ClientID = "test-codex-client"
	adminToken := adminBearer(t, application)

	started := startPKCE(t, application, adminToken, "codex", `{"name":"codex-plus"}`)
	if !strings.Contains(started.AuthorizationURL, "id_token_add_organizations=true") || !strings.Contains(started.AuthorizationURL, "codex_cli_simplified_flow=true") {
		t.Fatalf("authorization url = %s", started.AuthorizationURL)
	}
	application.pkceOAuthMu.Lock()
	state := application.pkceOAuthFlows[started.FlowID].State
	application.pkceOAuthMu.Unlock()
	callback := "http://localhost:1455/auth/callback?code=codex-code&state=" + state
	complete := exchangePKCE(t, application, adminToken, "codex", started.FlowID, callback)
	if complete.Code != http.StatusCreated {
		t.Fatalf("exchange status=%d body=%s", complete.Code, complete.Body.String())
	}
	accounts, err := repo.ListSubscriptions(context.Background())
	if err != nil || len(accounts) != 1 {
		t.Fatalf("accounts=%+v err=%v", accounts, err)
	}
	credentials, err := repo.Credentials(context.Background(), accounts[0])
	if err != nil {
		t.Fatal(err)
	}
	if credentials.AccessToken != "codex-access" || credentials.ChatGPTAccountID != "acct_123" || credentials.OrganizationID != "org_789" || credentials.Email != "plus@example.com" {
		t.Fatalf("credentials = %+v", credentials)
	}
}

func TestPKCEOAuthRejectsStateMismatch(t *testing.T) {
	application, _ := testServer(t, "https://example.invalid/responses")
	adminToken := adminBearer(t, application)
	started := startPKCE(t, application, adminToken, "claude", `{"name":"claude-mismatch"}`)
	recorder := exchangePKCE(t, application, adminToken, "claude", started.FlowID, "code#wrong-state")
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "oauth_state_mismatch") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestExpiredClaudeOAuthTokenRefreshesBeforeProxy(t *testing.T) {
	var refreshed atomic.Bool
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]string
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["grant_type"] != "refresh_token" || payload["refresh_token"] != "old-refresh" {
			t.Errorf("refresh payload = %v", payload)
		}
		refreshed.Store(true)
		writeJSON(t, w, http.StatusOK, map[string]any{
			"access_token": "new-claude-access", "refresh_token": "new-claude-refresh", "expires_in": 3600,
		})
	}))
	defer oauth.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer new-claude-access" {
			t.Errorf("upstream authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg-1"}`)
	}))
	defer upstream.Close()

	application, repo := testServer(t, upstream.URL)
	application.cfg.ClaudeOAuth.TokenURL = oauth.URL
	application.cfg.Providers.ClaudeAPI = upstream.URL
	expired := time.Now().Add(-time.Minute)
	_, key := createSubscriptionWithKey(t, repo, repository.CreateSubscriptionParams{
		Name: "expired-claude", Provider: model.ProviderClaude, AuthType: "oauth",
		Credentials:    model.Credentials{AccessToken: "old-access", RefreshToken: "old-refresh", ClientID: "test-claude-client"},
		TokenExpiresAt: &expired,
	})
	request := httptest.NewRequest(http.MethodPost, "/claude/v1/messages", strings.NewReader(`{"model":"claude-sonnet","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Authorization", "Bearer "+key)
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !refreshed.Load() {
		t.Fatal("Claude refresh endpoint was not called")
	}
	accounts, err := repo.ListSubscriptions(context.Background())
	if err != nil || len(accounts) != 1 {
		t.Fatalf("accounts=%+v err=%v", accounts, err)
	}
	credentials, err := repo.Credentials(context.Background(), accounts[0])
	if err != nil {
		t.Fatal(err)
	}
	if credentials.AccessToken != "new-claude-access" || credentials.RefreshToken != "new-claude-refresh" {
		t.Fatalf("refreshed credentials = %+v", credentials)
	}
}

func TestCodexOAuthRetriesOnceAfterUpstreamUnauthorized(t *testing.T) {
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("grant_type") != "refresh_token" {
			t.Errorf("refresh form = %v", r.Form)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"access_token": "rotated-codex", "expires_in": 3600})
	}))
	defer oauth.Close()
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") == "Bearer stale-codex" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Authorization") != "Bearer rotated-codex" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(w, `{"id":"resp-1"}`)
	}))
	defer upstream.Close()

	application, repo := testServer(t, upstream.URL+"/responses")
	application.cfg.CodexOAuth.TokenURL = oauth.URL
	_, key := createSubscriptionWithKey(t, repo, repository.CreateSubscriptionParams{
		Name: "codex-without-expiry", Provider: model.ProviderCodex, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "stale-codex", RefreshToken: "refresh-token", ChatGPTAccountID: "acct_1"},
	})
	request := httptest.NewRequest(http.MethodPost, "/codex/v1/responses", strings.NewReader(`{"model":"gpt-5","input":"hello"}`))
	request.Header.Set("Authorization", "Bearer "+key)
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || calls.Load() != 2 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, calls.Load(), recorder.Body.String())
	}
}

func adminBearer(t *testing.T, application *Server) string {
	t.Helper()
	token, err := application.signer.issue("admin", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

type pkceStartResponse struct {
	FlowID           string `json:"flow_id"`
	AuthorizationURL string `json:"authorization_url"`
}

func startPKCE(t *testing.T, application *Server, adminToken, provider, body string) pkceStartResponse {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/providers/"+provider+"/oauth/start", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+adminToken)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("start status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var started pkceStartResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(recorder.Body.String(), "code_verifier") {
		t.Fatalf("start response leaked verifier: %s", recorder.Body.String())
	}
	return started
}

func exchangePKCE(t *testing.T, application *Server, adminToken, provider, flowID, code string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"flow_id": flowID, "code": code})
	request := httptest.NewRequest(http.MethodPost, "/api/providers/"+provider+"/oauth/exchange", strings.NewReader(string(body)))
	request.Header.Set("Authorization", "Bearer "+adminToken)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	return recorder
}

func testJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload, _ := json.Marshal(claims)
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}
