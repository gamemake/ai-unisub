package adapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ai-unisub/internal/oauth"
)

func TestClaudeAuthorizationURLAndJSONExchange(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content type=%q", r.Header.Get("Content-Type"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"claude-access","refresh_token":"claude-refresh","expires_in":3600}`))
	}))
	defer server.Close()
	adapter := NewClaude(ClaudeConfig{AuthorizeURL: "https://auth.test/authorize", TokenURL: server.URL, ClientID: "test-client", HTTPClient: server.Client()})
	started, err := adapter.BuildAuthorizationURL(context.Background(), oauth.AuthorizationInput{State: "state", CodeVerifier: "verifier", RedirectURI: "http://127.0.0.1/callback"})
	if err != nil || !strings.Contains(started.AuthorizationURL, "code_challenge_method=S256") || !strings.Contains(started.AuthorizationURL, "state=state") {
		t.Fatalf("authorization url=%q err=%v", started.AuthorizationURL, err)
	}
	credential, err := adapter.Exchange(context.Background(), "code", "state", "verifier", "http://127.0.0.1/callback")
	if err != nil || credential.AccessToken != "claude-access" || credential.RefreshToken != "claude-refresh" {
		t.Fatalf("credential=%+v err=%v", credential, err)
	}
}

func TestGrokDevicePendingAndToken(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/device/code") {
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(r.Form.Get("scope"), "offline_access") || !strings.Contains(r.Form.Get("scope"), "grok-cli:access") {
				t.Errorf("device scope=%q", r.Form.Get("scope"))
			}
			_, _ = w.Write([]byte(`{"device_code":"device","user_code":"ABCD","verification_uri":"https://auth.test/verify","expires_in":600}`))
			return
		}
		if requests == 2 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"grok-access","refresh_token":"grok-refresh","expires_in":3600}`))
	}))
	defer server.Close()
	adapter := NewGrok(GrokConfig{Issuer: server.URL, ClientID: "test-client", HTTPClient: server.Client()})
	start, err := adapter.StartDeviceAuthorization(context.Background(), oauth.DeviceStartInput{Service: oauth.OAuthServiceGrok})
	if err != nil || start.DeviceCode != "device" {
		t.Fatalf("start=%+v err=%v", start, err)
	}
	if _, err := adapter.PollDeviceToken(context.Background(), start.DeviceCode); err != oauth.ErrAuthorizationPending {
		t.Fatalf("pending err=%v", err)
	}
	credential, err := adapter.PollDeviceToken(context.Background(), start.DeviceCode)
	if err != nil || credential.AccessToken != "grok-access" {
		t.Fatalf("credential=%+v err=%v", credential, err)
	}
}
