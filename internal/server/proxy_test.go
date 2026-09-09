package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/config"
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
		if got := r.Header.Get("originator"); got != codexOriginator {
			t.Errorf("originator = %q", got)
		}
		if got := r.Header.Get("version"); got != codexClientVersion {
			t.Errorf("version = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != codexCLIUserAgent() {
			t.Errorf("user-agent = %q", got)
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
	account, key := createSubscriptionWithKey(t, repo, repository.CreateSubscriptionParams{
		Name: "codex", Provider: model.ProviderCodex,
		Credentials: model.Credentials{AccessToken: "upstream-token", ChatGPTAccountID: "account-123"},
	})
	if account.ID == 0 {
		t.Fatal("account was not created")
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

func TestProxyStripsProxyAuthorizationAndAcceptEncoding(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Proxy-Authorization"); got != "" {
			t.Errorf("Proxy-Authorization leaked: %q", got)
		}
		if got := r.Header.Get("Accept-Encoding"); got != "" {
			t.Errorf("Accept-Encoding leaked: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r1","usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}`)
	}))
	defer upstream.Close()

	application, repo := testServer(t, upstream.URL+"/responses")
	_, key := createSubscriptionWithKey(t, repo, repository.CreateSubscriptionParams{
		Name: "strip-headers", Provider: model.ProviderCodex,
		Credentials: model.Credentials{AccessToken: "upstream-token"},
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-test"}`))
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Proxy-Authorization", "Basic cHJveHk6c2VjcmV0")
	request.Header.Set("Accept-Encoding", "gzip")
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	logs, _, err := repo.ListRequestLogs(context.Background(), time.Local, repository.RequestLogFilter{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].InputTokens == nil || *logs[0].InputTokens != 4 {
		t.Fatalf("token stats = %+v", logs)
	}
}

func TestProxyRestoresCorrectContentLength(t *testing.T) {
	requestBody := []byte(`{"model":"gpt-test"}`)
	want := strconv.Itoa(len(requestBody))
	var gotContentLength string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentLength = r.Header.Get("Content-Length")
		if r.ContentLength != int64(len(requestBody)) {
			t.Errorf("ContentLength field = %d, want %d", r.ContentLength, len(requestBody))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r1","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	}))
	defer upstream.Close()

	application, repo := testServer(t, upstream.URL+"/responses")
	_, key := createSubscriptionWithKey(t, repo, repository.CreateSubscriptionParams{
		Name: "content-length", Provider: model.ProviderCodex,
		Credentials: model.Credentials{AccessToken: "upstream-token"},
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(requestBody))
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Length", "1") // wrong on purpose; gateway must correct it
	request.ContentLength = 1
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if gotContentLength != want {
		t.Fatalf("upstream Content-Length = %q, want %q", gotContentLength, want)
	}
	logs, _, err := repo.ListRequestLogs(context.Background(), time.Local, repository.RequestLogFilter{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("logs = %+v", logs)
	}
	detail, err := repo.GetRequestLog(context.Background(), logs[0].Day, logs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail.RequestHeaders, `"Content-Length"`) {
		t.Fatalf("request headers missing Content-Length: %s", detail.RequestHeaders)
	}
	if !strings.Contains(detail.UpstreamRequestHeaders, `"Content-Length":["`+want+`"]`) {
		t.Fatalf("upstream request headers missing corrected Content-Length: %s", detail.UpstreamRequestHeaders)
	}
}

func TestClaudePreservesClientUserAgent(t *testing.T) {
	const clientUA = "claude-cli/2.1.220 (external, cli)"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != clientUA {
			t.Errorf("user-agent = %q, want client UA preserved", got)
		}
		if got := r.Header.Get("anthropic-beta"); got != "claude-code-20250219,oauth-2025-04-20" {
			t.Errorf("anthropic-beta = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer claude-access" {
			t.Errorf("authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1"}`)
	}))
	defer upstream.Close()

	application, repo := testServer(t, upstream.URL+"/messages")
	application.cfg.Providers.ClaudeAPI = upstream.URL
	_, key := createSubscriptionWithKey(t, repo, repository.CreateSubscriptionParams{
		Name: "claude-ua", Provider: model.ProviderClaude,
		Credentials: model.Credentials{AccessToken: "claude-access"},
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("User-Agent", clientUA)
	request.Header.Set("anthropic-beta", "claude-code-20250219,oauth-2025-04-20")
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestProxyAcceptsXAPIKeyHeader(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	application, repo := testServer(t, upstream.URL+"/messages")
	application.cfg.Providers.ClaudeAPI = upstream.URL
	_, key := createSubscriptionWithKey(t, repo, repository.CreateSubscriptionParams{
		Name: "claude-x-api-key", Provider: model.ProviderClaude,
		Credentials: model.Credentials{AccessToken: "claude-access"},
	})

	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("x-api-key", key)
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("x-api-key status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	goog := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}`))
	goog.Header.Set("x-goog-api-key", key)
	googRecorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(googRecorder, goog)
	if googRecorder.Code != http.StatusOK {
		t.Fatalf("x-goog-api-key status=%d body=%s", googRecorder.Code, googRecorder.Body.String())
	}
}

func TestInjectProviderHeadersCodexAndClaudeUA(t *testing.T) {
	codexHeader := make(http.Header)
	injectProviderHeaders(codexHeader, model.ProviderCodex, model.Credentials{AccessToken: "t", ChatGPTAccountID: "a"}, "", routeResponses)
	if got := codexHeader.Get("User-Agent"); got != codexCLIUserAgent() {
		t.Fatalf("codex UA = %q", got)
	}
	if got := codexHeader.Get("originator"); got != codexOriginator {
		t.Fatalf("codex originator = %q", got)
	}
	if got := codexHeader.Get("version"); got != codexClientVersion {
		t.Fatalf("codex version = %q", got)
	}

	claudeHeader := make(http.Header)
	claudeHeader.Set("User-Agent", "claude-cli/2.1.200 (external, cli)")
	claudeHeader.Set("anthropic-beta", "claude-code-20250219,oauth-2025-04-20")
	injectProviderHeaders(claudeHeader, model.ProviderClaude, model.Credentials{AccessToken: "t"}, "", routeMessages)
	if got := claudeHeader.Get("User-Agent"); got != "claude-cli/2.1.200 (external, cli)" {
		t.Fatalf("claude UA overwritten = %q", got)
	}
	if got := claudeHeader.Get("anthropic-beta"); got != "claude-code-20250219,oauth-2025-04-20" {
		t.Fatalf("claude beta overwritten = %q", got)
	}

	emptyClaude := make(http.Header)
	injectProviderHeaders(emptyClaude, model.ProviderClaude, model.Credentials{AccessToken: "t"}, "", routeMessages)
	if got := emptyClaude.Get("User-Agent"); got != "" {
		t.Fatalf("claude UA should be preserved, got %q", got)
	}
	if got := emptyClaude.Get("anthropic-beta"); got != "" {
		t.Fatalf("claude beta should be preserved, got %q", got)
	}
}

func TestInjectProviderHeadersPassesAcceptThrough(t *testing.T) {
	cases := []struct {
		name     string
		provider model.Provider
		kind     routeKind
		accept   string
	}{
		{name: "claude", provider: model.ProviderClaude, kind: routeMessages, accept: "application/json"},
		{name: "codex", provider: model.ProviderCodex, kind: routeResponses, accept: "text/event-stream"},
		{name: "grok", provider: model.ProviderGrok, kind: routeResponses, accept: "application/json, text/event-stream"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			header := make(http.Header)
			header.Set("Accept", tc.accept)
			injectProviderHeaders(header, tc.provider, model.Credentials{AccessToken: "t", ChatGPTAccountID: "a"}, "https://cli-chat-proxy.grok.com/v1/responses", tc.kind)
			if got := header.Get("Accept"); got != tc.accept {
				t.Fatalf("Accept overwritten: got %q want %q", got, tc.accept)
			}
		})
	}

	empty := make(http.Header)
	injectProviderHeaders(empty, model.ProviderCodex, model.Credentials{AccessToken: "t", ChatGPTAccountID: "a"}, "", routeResponses)
	if got := empty.Get("Accept"); got != "" {
		t.Fatalf("Accept should stay empty when client omitted it, got %q", got)
	}
}

func TestClaudeOfficialClientHeadersArePreserved(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("beta") != "true" {
			t.Errorf("beta query = %q", r.URL.RawQuery)
		}
		if got := r.Header.Get("User-Agent"); got != "curl/8.0.0" {
			t.Errorf("user-agent = %q", got)
		}
		beta := r.Header.Get("anthropic-beta")
		if beta != "" {
			t.Errorf("anthropic-beta should be preserved, got %q", beta)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1"}`)
	}))
	defer upstream.Close()

	application, repo := testServer(t, upstream.URL+"/messages")
	application.cfg.Providers.ClaudeAPI = upstream.URL
	_, key := createSubscriptionWithKey(t, repo, repository.CreateSubscriptionParams{
		Name: "claude-official", Provider: model.ProviderClaude,
		Credentials: model.Credentials{AccessToken: "claude-access"},
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("User-Agent", "curl/8.0.0")
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestProxyStoresAnthropicUnifiedQuotaWindows(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("anthropic-ratelimit-unified-5h-utilization", "0.42")
		w.Header().Set("anthropic-ratelimit-unified-5h-reset", "1789000000")
		w.Header().Set("anthropic-ratelimit-unified-7d-utilization", "0.18")
		w.Header().Set("anthropic-ratelimit-unified-7d-reset", "1789600000")
		_, _ = io.WriteString(w, `{"id":"msg_1"}`)
	}))
	defer upstream.Close()

	application, repo := testServer(t, upstream.URL+"/messages")
	application.cfg.Providers.ClaudeAPI = upstream.URL
	account, key := createSubscriptionWithKey(t, repo, repository.CreateSubscriptionParams{
		Name: "claude-unified-quota", Provider: model.ProviderClaude,
		Credentials: model.Credentials{AccessToken: "claude-access"},
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Authorization", "Bearer "+key)
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	stored, err := repo.GetSubscription(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	var quota struct {
		Windows []struct {
			Name             string  `json:"name"`
			UsedPercent      float64 `json:"used_percent"`
			RemainingPercent float64 `json:"remaining_percent"`
			ResetAt          string  `json:"reset_at"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(stored.Quota, &quota); err != nil {
		t.Fatal(err)
	}
	if len(quota.Windows) != 2 {
		t.Fatalf("windows = %+v", quota.Windows)
	}
	byName := map[string]struct {
		Used    float64
		ResetAt string
	}{}
	for _, window := range quota.Windows {
		byName[window.Name] = struct {
			Used    float64
			ResetAt string
		}{Used: window.UsedPercent, ResetAt: window.ResetAt}
	}
	if byName["five_hour"].Used != 42 || byName["five_hour"].ResetAt == "" {
		t.Fatalf("five_hour = %+v", byName["five_hour"])
	}
	if byName["seven_day"].Used != 18 || byName["seven_day"].ResetAt == "" {
		t.Fatalf("seven_day = %+v", byName["seven_day"])
	}
}

func TestProxyStoresUpstreamRateLimitQuota(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-ratelimit-limit-requests", "100")
		w.Header().Set("x-ratelimit-remaining-requests", "73")
		w.Header().Set("x-ratelimit-reset-requests", "2m")
		w.Header().Set("x-ratelimit-limit-tokens", "10000")
		w.Header().Set("x-ratelimit-remaining-tokens", "8400")
		w.Header().Set("x-ratelimit-reset-tokens", "30s")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	application, repo := testServer(t, upstream.URL+"/responses")
	account, key := createSubscriptionWithKey(t, repo, repository.CreateSubscriptionParams{
		Name: "quota-codex", Provider: model.ProviderCodex,
		Credentials: model.Credentials{AccessToken: "upstream-token"},
	})

	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-test"}`))
	request.Header.Set("Authorization", "Bearer "+key)
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	stored, err := repo.GetSubscription(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	var quota struct {
		Request struct {
			Limit     float64 `json:"limit"`
			Remaining float64 `json:"remaining"`
			ResetAt   string  `json:"reset_at"`
		} `json:"request_quota"`
		Token struct {
			Limit     float64 `json:"limit"`
			Remaining float64 `json:"remaining"`
			ResetAt   string  `json:"reset_at"`
		} `json:"token_quota"`
	}
	if err := json.Unmarshal(stored.Quota, &quota); err != nil {
		t.Fatal(err)
	}
	if quota.Request.Limit != 100 || quota.Request.Remaining != 73 || quota.Request.ResetAt == "" {
		t.Fatalf("request quota = %+v", quota.Request)
	}
	if quota.Token.Limit != 10000 || quota.Token.Remaining != 8400 || quota.Token.ResetAt == "" {
		t.Fatalf("token quota = %+v", quota.Token)
	}
	if stored.QuotaCheckedAt == nil || stored.QuotaError != nil {
		t.Fatalf("quota metadata checked=%v error=%v", stored.QuotaCheckedAt, stored.QuotaError)
	}
}

func TestUnsafeResponsesSubpathsAreRejected(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("unsafe path reached upstream") }))
	defer upstream.Close()
	application, repo := testServer(t, upstream.URL+"/responses")
	_, key := createSubscriptionWithKey(t, repo, repository.CreateSubscriptionParams{
		Name: "grok", Provider: model.ProviderGrok, Credentials: model.Credentials{AccessToken: "token"},
	})
	for _, path := range []string{"/v1/responses/../home", "/v1/responses/a%252fb", "/v1/responses/a//b"} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"x"}`))
		request.Header.Set("Authorization", "Bearer "+key)
		recorder := httptest.NewRecorder()
		application.Handler().ServeHTTP(recorder, request)
		if recorder.Code < 400 {
			t.Errorf("unsafe path %q returned %d", path, recorder.Code)
		}
	}
}

func TestProxyRecordsHTTPAndTokens(t *testing.T) {
	requestBody := []byte(`{"model":"gpt-test","input":"hello"}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("request-id", "up-99")
		_, _ = io.WriteString(w, `{"id":"r1","model":"gpt-test","usage":{"input_tokens":15,"output_tokens":8,"total_tokens":23}}`)
	}))
	defer upstream.Close()

	application, repo := testServer(t, upstream.URL+"/responses")
	account, key := createSubscriptionWithKey(t, repo, repository.CreateSubscriptionParams{
		Name: "log-codex", Provider: model.ProviderCodex,
		Credentials: model.Credentials{AccessToken: "upstream-token"},
	})

	request := httptest.NewRequest(http.MethodPost, "/v1/responses?stream=0", bytes.NewReader(requestBody))
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("X-Custom", "trace")
	request.Header.Set("X-Forwarded-For", "198.51.100.20")
	request.RemoteAddr = "127.0.0.1:54321"
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	logs, total, err := repo.ListRequestLogs(context.Background(), time.Local, repository.RequestLogFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(logs) != 1 {
		t.Fatalf("logs total=%d len=%d", total, len(logs))
	}
	summary := logs[0]
	if summary.SubscriptionID == nil || *summary.SubscriptionID != account.ID || summary.Path != "/v1/responses" || summary.Query != "stream=0" || summary.ClientIP != "198.51.100.20" {
		t.Fatalf("summary = %+v", summary)
	}
	if summary.InputTokens == nil || *summary.InputTokens != 15 || summary.OutputTokens == nil || *summary.OutputTokens != 8 {
		t.Fatalf("tokens = in:%v out:%v", summary.InputTokens, summary.OutputTokens)
	}
	detail, err := repo.GetRequestLog(context.Background(), summary.Day, summary.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail.RequestBody, `"model":"gpt-test"`) {
		t.Fatalf("request body = %s", detail.RequestBody)
	}
	if !strings.Contains(detail.ResponseBody, `"id":"r1"`) {
		t.Fatalf("response body = %s", detail.ResponseBody)
	}
	if !strings.Contains(detail.RequestHeaders, "Bearer "+key) {
		t.Fatalf("authorization missing from request headers: %s", detail.RequestHeaders)
	}
	if !strings.Contains(detail.RequestHeaders, "X-Custom") {
		t.Fatalf("custom header missing: %s", detail.RequestHeaders)
	}
	if !strings.Contains(detail.UpstreamRequestHeaders, "X-Custom") || !strings.Contains(detail.UpstreamRequestHeaders, "upstream-token") {
		t.Fatalf("upstream request headers incomplete: %s", detail.UpstreamRequestHeaders)
	}
	if !strings.Contains(detail.ResponseHeaders, "up-99") {
		t.Fatalf("response headers missing request id: %s", detail.ResponseHeaders)
	}
	if !strings.Contains(detail.ResponseHeaders, "application/json") {
		t.Fatalf("response headers missing content type: %s", detail.ResponseHeaders)
	}

	usage, err := repo.UsageSummary(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Requests24H != 1 || usage.InputTokens24H != 15 || usage.OutputTokens24H != 8 || usage.TotalTokens24H != 23 {
		t.Fatalf("local usage = %+v", usage)
	}
}

func TestProxyRecordsSSETokens(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"model\":\"gpt-test\",\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n")
	}))
	defer upstream.Close()
	application, repo := testServer(t, upstream.URL+"/responses")
	_, key := createSubscriptionWithKey(t, repo, repository.CreateSubscriptionParams{
		Name: "sse-log", Provider: model.ProviderCodex,
		Credentials: model.Credentials{AccessToken: "upstream-token"},
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-test"}`))
	request.Header.Set("Authorization", "Bearer "+key)
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	logs, _, err := repo.ListRequestLogs(context.Background(), time.Local, repository.RequestLogFilter{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].InputTokens == nil || *logs[0].InputTokens != 3 || logs[0].OutputTokens == nil || *logs[0].OutputTokens != 2 {
		t.Fatalf("sse log = %+v", logs)
	}
}

func createSubscriptionWithKey(t *testing.T, repo *repository.Repository, p repository.CreateSubscriptionParams) (model.Subscription, string) {
	t.Helper()
	account, err := repo.CreateSubscription(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := repo.CreateAPIKey(context.Background(), repository.CreateAPIKeyParams{SubscriptionID: account.ID, Name: account.Name})
	if err != nil {
		t.Fatal(err)
	}
	return account, key
}

func testServer(t *testing.T, responsesURL string) (*Server, *repository.Repository) {
	t.Helper()
	db, err := database.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	repo := repository.New(db)
	t.Cleanup(func() { _ = repo.Close() })
	if _, err := repo.BootstrapAdmin(context.Background(), "admin", "test-password-ok"); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		ListenAddress: ":0", DatabasePath: "unused", AdminUsername: "admin", AdminPassword: "test-password",
		AdminTokenTTL: time.Hour, MaxRequestBodyBytes: 1 << 20, AllowTestUpstreams: true,
		Providers: config.ProviderURLs{
			ClaudeAPI: responsesURL, ClaudeModels: responsesURL,
			CodexAPI: responsesURL, CodexModels: responsesURL,
			GrokAPI: responsesURL, GrokModels: responsesURL,
		},
	}
	application := New(cfg, repo)
	t.Cleanup(func() { _ = application.Shutdown(context.Background()) })
	return application, repo
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
