package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ai-unisub/internal/database"
	"ai-unisub/internal/oauth"
)

func TestOAuthProvidersHandleAPICalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("X-Caller"); got != "test" {
			t.Errorf("caller header = %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"model":"test"}` {
			t.Errorf("body = %s", body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	db := database.NewMemoryDatabase()
	credential, _ := json.Marshal(oauth.OAuthCredential{AccessToken: "access-token"})
	if err := db.SaveCredential("credential-1", credential); err != nil {
		t.Fatal(err)
	}
	manager := oauth.NewManager(db)
	if err := manager.Register(&testOAuthAdapter{service: oauth.OAuthServiceClaude}); err != nil {
		t.Fatal(err)
	}
	p, err := NewClaudeProvider("provider-1", json.RawMessage(`{"credential_id":"credential-1"}`), manager)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, server.URL+"/v1/messages", io.NopCloser(strings.NewReader(`{"model":"test"}`)))
	req.Header.Set("X-Caller", "test")
	var trace *ProviderCallTrace
	p.Handle(req, func(value *ProviderCallTrace) { trace = value })
	if trace == nil {
		t.Fatal("provider did not record API call")
	}
	if trace.HTTPErrorCode != 0 {
		t.Fatalf("unexpected HTTP error: %d %s", trace.HTTPErrorCode, trace.HTTPErrorInfo)
	}
}

type testOAuthAdapter struct{ service string }

func (a *testOAuthAdapter) Service() string { return a.service }
func (a *testOAuthAdapter) Refresh(_ context.Context, credential *oauth.OAuthCredential) (*oauth.OAuthCredential, error) {
	return credential, nil
}
