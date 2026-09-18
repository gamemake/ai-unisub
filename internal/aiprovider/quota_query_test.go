package aiprovider

import (
	"ai-unisub/internal/oauth"
	"context"
	jsonv1 "encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

type quotaRefreshAdapter struct {
	calls   int
	service string
}

func (a *quotaRefreshAdapter) Service() string {
	if a.service != "" {
		return a.service
	}
	return oauth.OAuthServiceClaude
}
func (a *quotaRefreshAdapter) Refresh(context.Context, *oauth.OAuthCredential, *http.Client) (*oauth.OAuthCredential, error) {
	a.calls++
	return &oauth.OAuthCredential{AccessToken: "renewed", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func TestQuotaOAuthRecoveryAndRedirectRefusal(t *testing.T) {
	store := &quotaCredentialStore{raw: []byte(`{"access_token":"old","refresh_token":"refresh"}`)}
	manager := oauth.NewOAuthManager(store)
	adapter := &quotaRefreshAdapter{}
	if err := manager.Register(adapter); err != nil {
		t.Fatal(err)
	}
	p := &oauthAIProvider{manager: manager, config: AIProviderConfig{Kind: "subscription", Supplier: "anthropic", CredentialID: "cred"}}
	calls := 0
	p.client = &http.Client{Transport: quotaTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		status, body := 200, `{"five_hour":{"utilization":25}}`
		if calls == 1 {
			status, body = 401, `{"error":"expired"}`
		} else if r.Header.Get("Authorization") != "Bearer renewed" {
			t.Fatal("token not refreshed")
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	if _, err := p.quota(t.Context(), ""); err != nil || calls != 2 || adapter.calls != 1 {
		t.Fatal(err, calls, adapter.calls)
	}
	p.client.Transport = quotaTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://other.invalid/collect"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	if _, err := p.quota(t.Context(), ""); !errors.Is(err, ErrQuotaUpstream) || calls != 3 {
		t.Fatal("followed redirect", err, calls)
	}
	if len(p.GetCachedQuota().Subscription) != 1 || p.GetCachedQuota().Subscription[0].Usage != 25 {
		t.Fatal("redirect destroyed cache")
	}
	p.config.ProxyGroupID = 1
	if _, err := p.quota(t.Context(), ""); !errors.Is(err, ErrQuotaNotConfigured) || calls != 3 {
		t.Fatal("bypassed required proxy", err, calls)
	}
}

func TestQuotaConfigChangeInvalidatesInflightQuery(t *testing.T) {
	p := &oauthAIProvider{config: AIProviderConfig{ID: "test", Kind: "api", Supplier: "deepseek", AuthType: AuthTypeAPIKey, APIKey: "old"}}
	p.client = &http.Client{Transport: quotaTransport(func(r *http.Request) (*http.Response, error) {
		if err := p.update([]byte(`{"kind":"api","supplier":"deepseek","auth_type":"api_key","api_key":"new"}`)); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"10","granted_balance":"0","topped_up_balance":"10"}]}`))}, nil
	})}
	if data, err := p.quota(t.Context(), ""); data != nil || !errors.Is(err, ErrQuotaSuperseded) {
		t.Fatal(data, err)
	}
	if p.GetCachedQuota().CacheStatus != QuotaCacheMissing {
		t.Fatal("old account cache resurrected")
	}
}

type quotaTransport func(*http.Request) (*http.Response, error)

func TestQuotaPreservesRawFields(t *testing.T) {
	body := `{"five_hour": {"utilization":2.500e-1,"resets_at":"2030-01-01T08:00:00+08:00","label":"中文"},"seven_day":null,"extra_usage":{"used_credits":123}}`
	items, err := parseSupplierQuota("anthropic", []byte(body))
	want := []QuotaItem{
		{Name: "five_hour", Value: `{"utilization":2.500e-1,"resets_at":"2030-01-01T08:00:00+08:00","label":"中文"}`},
		{Name: "seven_day", Value: `null`},
		{Name: "extra_usage", Value: `{"used_credits":123}`},
	}
	if err != nil || !reflect.DeepEqual(items, want) {
		t.Fatalf("raw data changed: got %v, err %v; want %v", items, err, want)
	}
}
func (f quotaTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type quotaCredentialStore struct{ raw jsonv1.RawMessage }

func (s *quotaCredentialStore) LoadCredential(string) (jsonv1.RawMessage, error) { return s.raw, nil }
func (s *quotaCredentialStore) SaveCredential(_ string, raw jsonv1.RawMessage) error {
	s.raw = raw
	return nil
}
func (s *quotaCredentialStore) DeleteCredential(string) error { s.raw = nil; return nil }

func TestSupplierQuotaQueriesAndCache(t *testing.T) {
	for _, tc := range []struct{ supplier, kind, endpoint, body, first string }{
		{"deepseek", "api", "https://api.deepseek.com/user/balance", `{"balance_infos":[{"currency":"CNY","total_balance":"0.000001","granted_balance":"0","topped_up_balance":"0.000001"}],"is_available":true}`, "0.000001 CNY"},
		{"kimi", "api", "https://api.moonshot.cn/v1/users/me/balance", `{"status":true,"code":0,"scode":"0x0","data":{"available_balance":49.588940001,"cash_balance":0,"voucher_balance":49.588940001}}`, "true"},
		{"openai", "subscription", "https://chatgpt.com/backend-api/wham/usage", `{"rate_limit":{"primary_window":{"used_percent":0,"limit_window_seconds":18000,"reset_at":1893456000}}}`, `{"primary_window":{"used_percent":0,"limit_window_seconds":18000,"reset_at":1893456000}}`},
		{"anthropic", "subscription", "https://api.anthropic.com/api/oauth/usage", `{"five_hour":{"utilization":25.5,"resets_at":"2030-01-01T00:00:00Z"},"seven_day":null,"extra_usage":{"used_credits":123}}`, `{"utilization":25.5,"resets_at":"2030-01-01T00:00:00Z"}`},
	} {
		t.Run(tc.supplier, func(t *testing.T) {
			store := &quotaCredentialStore{raw: []byte(`{"access_token":"test-oauth","account_id":"account-1"}`)}
			p := &oauthAIProvider{manager: oauth.NewOAuthManager(store), config: AIProviderConfig{ID: "test", Kind: tc.kind, Supplier: tc.supplier, AuthType: AuthTypeAPIKey, APIKey: "test-key"}}
			if tc.kind == "subscription" {
				p.config.AuthType = AuthTypeOAuth
				p.config.CredentialID = "credential"
			}
			calls, status := 0, 200
			body := tc.body
			p.quotaSupplier = func(string) Supplier {
				return Supplier{SubscriptionUsageHeaderOverrides: map[string]string{"X-Test": "override"}, APIUsageHeaderOverrides: map[string]string{"X-Test": "override"}}
			}
			p.client = &http.Client{Transport: quotaTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "GET" || r.URL.String() != tc.endpoint || r.Header.Get("X-Test") != "override" {
					t.Fatalf("unexpected query: %v", r)
				}
				token := "test-key"
				if tc.kind == "subscription" {
					token = "test-oauth"
				}
				if r.Header.Get("Authorization") != "Bearer "+token {
					t.Fatal("authentication not injected")
				}
				if tc.supplier == "openai" && r.Header.Get("ChatGPT-Account-Id") != "account-1" {
					t.Fatal("missing account scope")
				}
				if tc.supplier == "openai" && (r.Header.Get("OpenAI-Beta") != "codex-1" || r.Header.Get("originator") != "Codex Desktop") {
					t.Fatal("missing Codex query identity")
				}
				if tc.supplier == "anthropic" && r.Header.Get("anthropic-beta") != "oauth-2025-04-20" {
					t.Fatal("missing beta header")
				}
				if tc.supplier == "anthropic" && r.UserAgent() != "claude-code/2.1.7" {
					t.Fatal("missing Claude query identity")
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			if p.GetCachedQuota().CacheStatus != QuotaCacheMissing || calls != 0 {
				t.Fatal("read fetched data")
			}
			result, err := p.quota(t.Context(), "")
			if err != nil {
				t.Fatalf("unexpected result: %+v, %v", result, err)
			}
			if tc.kind == "api" {
				if len(result.Subscription) != 0 || result.Items[0].Value != tc.first {
					t.Fatal(result)
				}
			} else {
				wantUsage := 0.0
				if tc.supplier == "anthropic" {
					wantUsage = 25.5
				}
				if len(result.Items) != 0 || len(result.Subscription) != 1 || result.Subscription[0].Usage != wantUsage || result.Subscription[0].TimeDimension != "5h" {
					t.Fatal(result)
				}
			}
			before := p.GetCachedQuota()
			cached := p.GetCachedQuota()
			if calls != 1 || !reflect.DeepEqual(result, cached) {
				t.Fatal("cache differs from fetch")
			}
			if tc.kind == "api" {
				result.Items[0].Value = "mutated"
				cached.Items[0].Value = "also mutated"
			} else {
				result.Subscription[0].Usage = 999
				cached.Subscription[0].Usage = 999
			}
			if !reflect.DeepEqual(before, p.GetCachedQuota()) {
				t.Fatal("cache aliases returned result")
			}
			status = 429
			if r, e := p.quota(t.Context(), ""); r != nil || !errors.Is(e, ErrQuotaRateLimited) {
				t.Fatal(r, e)
			}
			if !reflect.DeepEqual(before, p.GetCachedQuota()) {
				t.Fatal("failed refresh removed last success")
			}
			status, body = 200, `{}`
			if _, e := p.quota(t.Context(), ""); !errors.Is(e, ErrQuotaInvalidResponse) {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(before, p.GetCachedQuota()) {
				t.Fatal("empty response overwrote cache")
			}
			p.config.APIEndpoint = "https://custom.invalid/v1"
			beforeCalls := calls
			if _, e := p.quota(t.Context(), ""); !errors.Is(e, ErrQuotaNotConfigured) || calls != beforeCalls {
				t.Fatal("custom credential leaked", e)
			}
		})
	}
}

func TestQuotaCacheOrderPartialFreshnessAndInvalidation(t *testing.T) {
	var cache quotaCache
	old := cache.begin()
	old.observed = time.Now().Add(-2 * quotaCacheTTL)
	cache.put(old, []QuotaItem{{Name: "5-hour used", Value: "10%"}, {Name: "Weekly used", Value: "20%"}}, false)
	newer := cache.begin()
	cache.put(newer, []QuotaItem{{Name: "5-hour used", Value: "30%"}}, true)
	result := cache.get()
	if result.CacheStatus != QuotaCacheStale || len(result.Items) != 2 {
		t.Fatal("partial update made all data fresh", result)
	}
	cache.put(old, []QuotaItem{{Name: "5-hour used", Value: "5%"}}, true)
	if cache.get().Items[0].Value != "30%" {
		t.Fatal("out-of-order response overwrote new data")
	}
	cache.invalidate()
	cache.put(newer, []QuotaItem{{Name: "5-hour used", Value: "30%"}}, false)
	if cache.get().CacheStatus != QuotaCacheMissing {
		t.Fatal("old generation restored cache")
	}
}

func TestQuotaHeaderOverridesAndPassiveUpdates(t *testing.T) {
	for _, key := range []string{"Authorization", "authorization", "X-Api-Key", "Cookie", "Host", "ChatGPT-Account-Id", "Connection"} {
		if validQuotaHeaders(map[string]string{key: "secret"}) {
			t.Fatalf("accepted protected header %s", key)
		}
	}
	if validQuotaHeaders(map[string]string{"X-Test": "x\r\nAuthorization: secret"}) {
		t.Fatal("accepted header injection")
	}
	claude := http.Header{}
	claude.Set("anthropic-ratelimit-unified-5h-utilization", "0.25")
	items := subscriptionHeaderItems(AIProviderConfig{Kind: "subscription"}, "claude", claude)
	if len(items) != 1 || items[0].Name != "Anthropic-Ratelimit-Unified-5h-Utilization" || items[0].Value != "0.25" {
		t.Fatal(items)
	}
	if len(subscriptionHeaderItems(AIProviderConfig{Kind: "api"}, "claude", claude)) != 0 {
		t.Fatal("API rate limit treated as balance")
	}
	codex := http.Header{}
	codex.Set("x-codex-primary-used-percent", "0")
	codex.Set("x-codex-primary-window-minutes", "60")
	items = subscriptionHeaderItems(AIProviderConfig{Kind: "subscription"}, "codex", codex)
	if len(items) != 2 || items[0].Name != "X-Codex-Primary-Used-Percent" || items[0].Value != "0" || items[1].Value != "60" {
		t.Fatal(items)
	}
}
