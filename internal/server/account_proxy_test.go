package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ai-unisub/ai-unisub/internal/model"
	"github.com/ai-unisub/ai-unisub/internal/repository"
)

func TestNormalizeProxyURL(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080"},
		{"http://user:pass@proxy.example:3128", "http://user:pass@proxy.example:3128"},
		{"socks5://127.0.0.1:1080", "socks5h://127.0.0.1:1080"},
		{"socks5h://127.0.0.1:1080", "socks5h://127.0.0.1:1080"},
	} {
		normalized, err := normalizeProxyURL(tc.raw)
		if err != nil || normalized != tc.want {
			t.Errorf("normalizeProxyURL(%q) = %q, %v; want %q", tc.raw, normalized, err, tc.want)
		}
	}
	for _, raw := range []string{
		"https://127.0.0.1:8080",
		"socks5://127.0.0.1",
		"http://127.0.0.1:8080/path",
		"not-a-url",
	} {
		if _, err := normalizeProxyURL(raw); err == nil {
			t.Errorf("normalizeProxyURL(%q) unexpectedly succeeded", raw)
		}
	}
}

func TestDirectAccountClientIgnoresEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")
	t.Setenv("http_proxy", "http://127.0.0.1:9")
	t.Setenv("https_proxy", "http://127.0.0.1:9")

	client, err := newClientForProxy("")
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected *http.Transport")
	}
	if transport.Proxy != nil {
		req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
		if proxyURL, err := transport.Proxy(req); err != nil || proxyURL != nil {
			t.Fatalf("direct transport still uses proxy func result=%v err=%v", proxyURL, err)
		}
	}
}

func TestHTTPClientsAreIsolatedByAccount(t *testing.T) {
	application, _ := testServer(t, "https://example.invalid/responses")
	first := model.Account{ID: 101, Provider: model.ProviderCodex}
	second := model.Account{ID: 202, Provider: model.ProviderCodex}

	firstClient, err := application.clientForAccount(first)
	if err != nil {
		t.Fatal(err)
	}
	firstAgain, err := application.clientForAccount(first)
	if err != nil {
		t.Fatal(err)
	}
	secondClient, err := application.clientForAccount(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstClient != firstAgain {
		t.Fatal("the same account did not reuse its HTTP client")
	}
	if firstClient == secondClient || firstClient.Transport == secondClient.Transport {
		t.Fatal("different accounts shared an HTTP client or transport")
	}
}

func TestAccountHTTPProxy(t *testing.T) {
	requestBody := []byte(`{"model":"gpt-test","input":"hello"}`)
	proxyReached := false
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyReached = true
		if r.URL.Host != "upstream.example" || r.URL.Path != "/responses" {
			t.Errorf("proxy target = %s", r.URL.String())
		}
		body, _ := io.ReadAll(r.Body)
		if !bytes.Equal(body, requestBody) {
			t.Errorf("body changed: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"response-through-proxy"}`)
	}))
	defer proxyServer.Close()

	application, repo := testServer(t, "http://upstream.example/responses")
	_, key := createAccountWithKey(t, repo, repository.CreateAccountParams{
		Name: "proxied-codex", Provider: model.ProviderCodex, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "upstream-token", ChatGPTAccountID: "account-123"},
		ProxyURL:    proxyServer.URL,
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(requestBody))
	request.Header.Set("Authorization", "Bearer "+key)
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !proxyReached {
		t.Fatalf("status=%d proxy_reached=%v body=%s", recorder.Code, proxyReached, recorder.Body.String())
	}
}
