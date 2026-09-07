package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSanitizeHeadersJSONRedactsSecretsAndKeepsOthers(t *testing.T) {
	encoded := sanitizeHeadersJSON(http.Header{
		"Authorization": []string{"Bearer secret-token"},
		"X-Api-Key":     []string{"sk-live"},
		"Cookie":        []string{"session=abc"},
		"Content-Type":  []string{"application/json"},
		"X-Custom":      []string{"trace", "span"},
	})
	for _, leaked := range []string{"secret-token", "sk-live", "session=abc"} {
		if strings.Contains(encoded, leaked) {
			t.Fatalf("secret leaked in %s", encoded)
		}
	}
	for _, marker := range []string{`"[redacted]"`, `"Content-Type"`, `"application/json"`, `"X-Custom"`, `"trace"`, `"span"`} {
		if !strings.Contains(encoded, marker) {
			t.Fatalf("missing %s in %s", marker, encoded)
		}
	}
}

func TestSanitizeHeadersJSONEmpty(t *testing.T) {
	if got := sanitizeHeadersJSON(nil); got != "{}" {
		t.Fatalf("nil headers = %q", got)
	}
	if got := sanitizeHeadersJSON(http.Header{}); got != "{}" {
		t.Fatalf("empty headers = %q", got)
	}
}

func TestSanitizeOutboundURLRedactsSecrets(t *testing.T) {
	raw, err := url.Parse("https://user:pass@example.com/oauth/token?code=abc&state=ok&access_token=secret&format=credits")
	if err != nil {
		t.Fatal(err)
	}
	got := sanitizeOutboundURL(raw)
	for _, leaked := range []string{"pass", "abc", "secret"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("secret %q leaked in %s", leaked, got)
		}
	}
	if !strings.Contains(got, "[redacted]") && !strings.Contains(got, "%5Bredacted%5D") {
		t.Fatalf("missing redaction marker in %s", got)
	}
	for _, marker := range []string{"state=ok", "format=credits", "example.com/oauth/token"} {
		if !strings.Contains(got, marker) {
			t.Fatalf("missing %s in %s", marker, got)
		}
	}
}

func TestDoHTTPLogsOutboundRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	defer slog.SetDefault(previous)

	request, err := http.NewRequest(http.MethodPost, upstream.URL+"/v1/oauth/token?code=secret-code", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := doHTTP(upstream.Client(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", response.StatusCode)
	}
	logged := buf.String()
	if !strings.Contains(logged, "http_outbound") || !strings.Contains(logged, "status=201") {
		t.Fatalf("missing outbound log: %s", logged)
	}
	if strings.Contains(logged, "secret-code") {
		t.Fatalf("authorization code leaked in log: %s", logged)
	}
}

func TestDoHTTPLogsOutboundErrors(t *testing.T) {
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	defer slog.SetDefault(previous)

	request, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:1/", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = doHTTP(&http.Client{}, request)
	if err == nil {
		t.Fatal("expected connection error")
	}
	logged := buf.String()
	if !strings.Contains(logged, "http_outbound") || !strings.Contains(logged, "level=WARN") {
		t.Fatalf("missing outbound warn log: %s", logged)
	}
}
