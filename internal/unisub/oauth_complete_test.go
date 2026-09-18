package unisub

import (
	"ai-unisub/internal/database"
	"ai-unisub/internal/oauth"
	"ai-unisub/internal/service"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

type localPKCE struct{}

func (*localPKCE) Service() string { return "codex" }
func (*localPKCE) Refresh(context.Context, *oauth.OAuthCredential, *http.Client) (*oauth.OAuthCredential, error) {
	return nil, fmt.Errorf("not used")
}
func (*localPKCE) BuildAuthorizationURL(_ context.Context, in oauth.AuthorizationInput) (oauth.AuthorizationResult, error) {
	return oauth.AuthorizationResult{AuthorizationURL: "https://example.invalid/authorize?state=" + in.State}, nil
}
func (*localPKCE) Exchange(_ context.Context, code, state, verifier, redirect string, _ *http.Client) (*oauth.OAuthCredential, error) {
	if code != "valid-code" || state == "" || verifier == "" || redirect == "" {
		return nil, fmt.Errorf("invalid exchange")
	}
	return &oauth.OAuthCredential{AccessToken: "local-oauth-token"}, nil
}

type testOAuthContext struct {
	service.ModuleContext
	manager *oauth.OAuthManager
}

func (c testOAuthContext) OAuth() *oauth.OAuthManager { return c.manager }

type testOAuthModule struct {
	*OAuthFlowModule
	manager *oauth.OAuthManager
}

func (m *testOAuthModule) Init(ctx service.ModuleContext) error {
	return m.OAuthFlowModule.Init(testOAuthContext{ctx, m.manager})
}

func TestOAuthCompleteAndCallbackAreBoundToOwner(t *testing.T) {
	db := testDatabase(t)
	srv, err := service.NewWithDependencies(service.Config{DatabaseURL: "sqlite::memory:"}, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	manager := oauth.NewManager(db)
	_ = manager.Register(&localPKCE{})
	if err := srv.AddModule(&testOAuthModule{NewOAuthFlowModule(), manager}); err != nil {
		t.Fatal(err)
	}
	users := []*database.PersistedUser{{Name: "alice", Enabled: true, Role: database.UserRoleAdmin}, {Name: "bob", Enabled: true, Role: database.UserRoleAdmin}}
	cookies := make([]*http.Cookie, 2)
	for i, u := range users {
		_ = db.SaveUser(u)
		token, err := srv.Auth().CreateSession(u)
		if err != nil {
			t.Fatal(err)
		}
		cookies[i] = &http.Cookie{Name: "session", Value: token}
	}
	start := func() (string, string) {
		t.Helper()
		out := appRequest(srv, "POST", "/api/oauth/codex/start", "{}", cookies[0])
		if out.Code != 200 {
			t.Fatalf("start: %s", out.Body.String())
		}
		var result oauth.StartResult
		_ = json.Unmarshal(out.Body.Bytes(), &result)
		parsed, _ := url.Parse(result.AuthorizationURL)
		return result.SessionID, parsed.Query().Get("state")
	}
	session, state := start()
	path := "/api/oauth/codex/complete/" + session
	payload := fmt.Sprintf(`{"code":"valid-code","state":%q}`, state)
	if out := appRequest(srv, "POST", path, payload, cookies[1]); out.Code != 400 {
		t.Fatalf("other user completed session: %d", out.Code)
	}
	if out := appRequest(srv, "POST", path, `{"code":"valid-code","state":"wrong"}`, cookies[0]); out.Code != 400 {
		t.Fatalf("wrong state accepted: %d", out.Code)
	}
	out := appRequest(srv, "POST", path, payload, cookies[0])
	if out.Code != 200 {
		t.Fatalf("complete: %d %s", out.Code, out.Body.String())
	}
	var result struct {
		ID string `json:"result_id"`
	}
	_ = json.Unmarshal(out.Body.Bytes(), &result)
	if out := appRequest(srv, "GET", "/api/oauth/results/"+result.ID, "", cookies[1]); out.Code != 404 {
		t.Fatal("other user read credential")
	}
	if out := appRequest(srv, "GET", "/api/oauth/results/"+result.ID, "", cookies[0]); out.Code != 200 {
		t.Fatal("owner could not read credential")
	}
	if out := appRequest(srv, "GET", "/api/oauth/results/"+result.ID, "", cookies[0]); out.Code != 404 {
		t.Fatal("result was reusable")
	}
	session, state = start()
	out = appRequest(srv, "GET", "/auth/callback?code=valid-code&state="+url.QueryEscape(state), "", nil)
	if out.Code != 200 {
		t.Fatal("callback failed")
	}
	if out := appRequest(srv, "GET", "/api/oauth/codex/status/"+session, "", cookies[1]); out.Code != 400 {
		t.Fatal("callback result visible to another user")
	}
	out = appRequest(srv, "GET", "/api/oauth/codex/status/"+session, "", cookies[0])
	_ = json.Unmarshal(out.Body.Bytes(), &result)
	if out.Code != 200 || result.ID == "" {
		t.Fatalf("owner cannot discover completed callback: %s", out.Body.String())
	}
}
