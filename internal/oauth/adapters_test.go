package oauth

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIAdapterPKCEExchangeAndClaims(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"user@example.test","https://api.openai.com/auth":{"chatgpt_account_id":"account"}}`))
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Error(err)
		}
		if request.Form.Get("code_verifier") != "verifier" || request.Header.Get("originator") != "codex-tui" {
			t.Errorf("unexpected OpenAI request: form=%v headers=%v", request.Form, request.Header)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"access_token":"access","refresh_token":"refresh","token_type":"Bearer","expires_in":3600,"id_token":"header.` + payload + `.signature"}`))
	}))
	defer server.Close()
	adapter := NewOpenAI(OpenAIConfig{AuthorizeURL: "https://auth.test/authorize", TokenURL: server.URL, ClientID: "client", HTTPClient: server.Client()})
	authorization, err := adapter.BuildAuthorizationURL(t.Context(), AuthorizationInput{State: "state", CodeVerifier: "verifier", RedirectURI: "http://127.0.0.1/callback"})
	if err != nil || !strings.Contains(authorization.AuthorizationURL, "code_challenge_method=S256") || !strings.Contains(authorization.AuthorizationURL, "state=state") {
		t.Fatalf("authorization=%+v err=%v", authorization, err)
	}
	credential, err := adapter.Exchange(t.Context(), "code", "state", "verifier", "http://127.0.0.1/callback", nil)
	if err != nil {
		t.Fatal(err)
	}
	if credential.AccessToken != "access" || credential.AccountID != "account" || credential.Email != "user@example.test" {
		t.Fatalf("unexpected credential: %+v", credential)
	}
}

func TestAnthropicAdapterJSONExchange(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content type=%q", request.Header.Get("Content-Type"))
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"access_token":"access","refresh_token":"refresh","expires_in":3600}`))
	}))
	defer server.Close()
	adapter := NewAnthropic(AnthropicConfig{AuthorizeURL: "https://auth.test/authorize", TokenURL: server.URL, ClientID: "client", HTTPClient: server.Client()})
	authorization, err := adapter.BuildAuthorizationURL(t.Context(), AuthorizationInput{State: "state", CodeVerifier: "verifier", RedirectURI: "http://127.0.0.1/callback"})
	if err != nil || !strings.Contains(authorization.AuthorizationURL, "code_challenge_method=S256") || !strings.Contains(authorization.AuthorizationURL, "state=state") {
		t.Fatalf("authorization=%+v err=%v", authorization, err)
	}
	credential, err := adapter.Exchange(t.Context(), "code", "state", "verifier", "http://127.0.0.1/callback", nil)
	if err != nil || credential.AccessToken != "access" || credential.RefreshToken != "refresh" {
		t.Fatalf("credential=%+v err=%v", credential, err)
	}
}

func TestXAIAdapterDevicePendingAndToken(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		response.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(request.URL.Path, "/device/code") {
			if err := request.ParseForm(); err != nil {
				t.Error(err)
			}
			if !strings.Contains(request.Form.Get("scope"), "grok-cli:access") {
				t.Errorf("scope=%q", request.Form.Get("scope"))
			}
			_, _ = response.Write([]byte(`{"device_code":"device","user_code":"ABCD","verification_uri":"https://auth.test/verify","expires_in":600}`))
			return
		}
		if requests == 2 {
			response.WriteHeader(http.StatusBadRequest)
			_, _ = response.Write([]byte(`{"error":"authorization_pending"}`))
			return
		}
		_, _ = response.Write([]byte(`{"access_token":"access","refresh_token":"refresh","expires_in":3600}`))
	}))
	defer server.Close()
	adapter := NewXAI(XAIConfig{Issuer: server.URL, ClientID: "client", HTTPClient: server.Client()})
	started, err := adapter.StartDeviceAuthorization(t.Context(), DeviceStartInput{Service: OAuthServiceXAI})
	if err != nil || started.DeviceCode != "device" {
		t.Fatalf("started=%+v err=%v", started, err)
	}
	if _, err := adapter.PollDeviceToken(t.Context(), started.DeviceCode, nil); !errors.Is(err, ErrAuthorizationPending) {
		t.Fatalf("expected pending, got %v", err)
	}
	credential, err := adapter.PollDeviceToken(t.Context(), started.DeviceCode, nil)
	if err != nil || credential.AccessToken != "access" {
		t.Fatalf("credential=%+v err=%v", credential, err)
	}
}
