package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/config"
	"github.com/ai-unisub/ai-unisub/internal/cryptox"
	"github.com/ai-unisub/ai-unisub/internal/database"
	"github.com/ai-unisub/ai-unisub/internal/model"
	"github.com/ai-unisub/ai-unisub/internal/repository"
)

func TestCodexSSEProxyIsolationAndPassthrough(t *testing.T) {
	requestBody := []byte(`{"model":"gpt-test","input":"hello","stream":true}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("upstream path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer upstream-token" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("chatgpt-account-id"); got != "account-123" {
			t.Errorf("account header = %q", got)
		}
		if got := r.Header.Get("Cookie"); got != "" {
			t.Errorf("downstream cookie leaked: %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		if !bytes.Equal(body, requestBody) {
			t.Errorf("body changed: %s", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "event: response.created\ndata: {\"id\":\"r1\"}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		_, _ = io.WriteString(w, "event: response.completed\ndata: {\"id\":\"r1\"}\n\n")
	}))
	defer upstream.Close()

	application, repo := testServer(t, upstream.URL+"/responses")
	account, key, err := repo.CreateAccount(context.Background(), repository.CreateAccountParams{
		Name: "codex", Provider: model.ProviderCodex, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "upstream-token", ChatGPTAccountID: "account-123"},
	})
	if err != nil || account.ID == 0 {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(requestBody))
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Cookie", "must-not-leak=1")
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("content type=%q", got)
	}
	if !strings.Contains(recorder.Body.String(), "response.completed") {
		t.Fatalf("SSE not relayed: %s", recorder.Body.String())
	}

	mismatch := httptest.NewRequest(http.MethodPost, "/grok/v1/responses", bytes.NewReader(requestBody))
	mismatch.Header.Set("Authorization", "Bearer "+key)
	mismatchRecorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(mismatchRecorder, mismatch)
	if mismatchRecorder.Code != http.StatusForbidden {
		t.Fatalf("provider mismatch status=%d", mismatchRecorder.Code)
	}
}

func TestUnsafeResponsesSubpathsAreRejected(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("unsafe path reached upstream") }))
	defer upstream.Close()
	application, repo := testServer(t, upstream.URL+"/responses")
	_, key, err := repo.CreateAccount(context.Background(), repository.CreateAccountParams{
		Name: "grok", Provider: model.ProviderGrok, AuthType: "oauth", Credentials: model.Credentials{AccessToken: "token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/responses/../admin", "/v1/responses/a%252fb", "/v1/responses/a//b"} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"x"}`))
		request.Header.Set("Authorization", "Bearer "+key)
		recorder := httptest.NewRecorder()
		application.Handler().ServeHTTP(recorder, request)
		if recorder.Code < 400 {
			t.Errorf("unsafe path %q returned %d", path, recorder.Code)
		}
	}
}

func testServer(t *testing.T, responsesURL string) (*Server, *repository.Repository) {
	t.Helper()
	key := bytes.Repeat([]byte{9}, 32)
	cipher, err := cryptox.New(key)
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	repo := repository.New(db, cipher, "test-v1")
	t.Cleanup(func() { _ = repo.Close() })
	cfg := config.Config{
		ListenAddress: ":0", DatabasePath: "unused", MasterKey: key, CredentialKeyID: "test-v1",
		AdminTokenTTL: time.Hour, MaxRequestBodyBytes: 1 << 20, AllowTestUpstreams: true,
		Providers: config.ProviderURLs{
			ClaudeAPI: responsesURL, ClaudeModels: responsesURL,
			CodexAPI: responsesURL, CodexModels: responsesURL,
			GrokAPI: responsesURL, GrokModels: responsesURL,
		},
	}
	return New(cfg, repo), repo
}

func TestAdminTokenRoundTrip(t *testing.T) {
	signer := tokenSigner{key: []byte("a sufficiently long signing key")}
	token, err := signer.issue("admin", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := signer.verify(token)
	if err != nil || claims.Subject != "admin" {
		t.Fatalf("claims=%+v err=%v", claims, err)
	}
	parts := strings.Split(token, ".")
	payload, _ := json.Marshal(adminClaims{Subject: "attacker", ExpiresAt: time.Now().Add(time.Hour).Unix()})
	parts[1] = rawURL(payload)
	if _, err := signer.verify(strings.Join(parts, ".")); err == nil {
		t.Fatal("tampered admin token accepted")
	}
}
