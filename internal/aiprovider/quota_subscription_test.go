package aiprovider

import (
	"ai-unisub/internal/oauth"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

const grokWeeklyFixture = `{"config":{"currentPeriod":{"type":"WEEKLY","start":"2026-07-09T03:25:00Z","end":"2026-07-16T03:25:00Z"},"creditUsagePercent":2.000,"productUsage":[{"product":"Api","quotaPercent":2}],"prepaidBalance":{"val":"12.340000000000001"},"onDemandCap":{"val":100},"onDemandUsed":{"val":5}}}`
const grokMonthlyFixture = `{"config":{"monthlyLimit":{"val":15000},"used":{"val":"78"},"billingPeriodStart":"2026-07-01T00:00:00Z","billingPeriodEnd":"2026-08-01T00:00:00Z"}}`

func newGrokQuotaTestProvider(t *testing.T) *GrokAIProvider {
	t.Helper()
	manager := oauth.NewOAuthManager(&quotaCredentialStore{raw: []byte(`{"access_token":"grok-token","refresh_token":"refresh"}`)})
	p, err := NewGrokAIProvider(1, ProviderData{Config: []byte(`{"kind":"subscription","supplier":"grok","credential_id":"credential"}`)}, manager)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGrokBillingFetchPreservesBothSourcesAndCache(t *testing.T) {
	p := newGrokQuotaTestProvider(t)
	p.quotaSupplier = func(string) Supplier {
		return Supplier{SubscriptionUsageHeaderOverrides: map[string]string{"X-Grok-Client-Version": "test-version"}}
	}
	var urls []string
	monthlyStatus := http.StatusOK
	weeklyBody := grokWeeklyFixture
	p.client = &http.Client{Transport: quotaTransport(func(r *http.Request) (*http.Response, error) {
		urls = append(urls, r.URL.String())
		if r.Method != http.MethodGet || r.URL.Host != "cli-chat-proxy.grok.com" || r.URL.Path != "/v1/billing" {
			t.Fatalf("unexpected billing request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer grok-token" || r.Header.Get("x-xai-token-auth") != "xai-grok-cli" || r.Header.Get("x-grok-client-version") != "test-version" || !strings.Contains(r.UserAgent(), "grok-pager/") {
			t.Fatal("missing OAuth or CLI identity")
		}
		body, status := weeklyBody, http.StatusOK
		if r.URL.RawQuery == "" {
			body, status = grokMonthlyFixture, monthlyStatus
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	result, err := p.FetchQuota(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := []SubscriptionQuotaItem{
		{TimeDimension: "weekly", Usage: 2, ResetAt: time.Date(2026, 7, 16, 3, 25, 0, 0, time.UTC)},
		{TimeDimension: "monthly", Usage: 0.52, ResetAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)},
	}
	if len(result.Items) != 0 || !reflect.DeepEqual(result.Subscription, want) || !reflect.DeepEqual(result, p.GetCachedQuota()) || len(urls) != 2 {
		t.Fatalf("unexpected normalized billing quota: %+v, %v", result, urls)
	}
	before := p.GetCachedQuota()
	weeklyBody = `{"config":{"creditUsagePercent":75}}`
	monthlyStatus = http.StatusTooManyRequests
	if got, err := p.FetchQuota(t.Context()); got != nil || !errors.Is(err, ErrQuotaRateLimited) {
		t.Fatal(got, err)
	}
	if !reflect.DeepEqual(before, p.GetCachedQuota()) {
		t.Fatal("partial refresh replaced old snapshot or changed freshness")
	}
	p.quotaCache.invalidate()
	if _, err := p.FetchQuota(t.Context()); !errors.Is(err, ErrQuotaRateLimited) || p.GetCachedQuota().CacheStatus != QuotaCacheMissing {
		t.Fatal("first partial failure fabricated a complete snapshot", err)
	}
}

func TestGrokBillingValidation(t *testing.T) {
	for _, tc := range []struct {
		body    string
		monthly bool
		valid   bool
	}{
		{grokWeeklyFixture, false, true},
		{grokMonthlyFixture, true, true},
		{`{"config":{"creditUsagePercent":0}}`, false, true},
		{`{"config":{"monthlyLimit":0,"used":"0.00000000000000001"}}`, true, true},
		{`{"config":{"monthlyLimit":"12345678901234567890.001","used":0}}`, true, true},
		{grokWeeklyFixture, true, false},
		{grokMonthlyFixture, false, false},
		{`{"config":null}`, false, false},
		{`{"config":{}}`, false, false},
		{`{"config":{"creditUsagePercent":"0"}}`, false, false},
		{`{"config":{"creditUsagePercent":-1}}`, false, false},
		{`{"config":{"creditUsagePercent":0,"productUsage":[null]}}`, false, false},
		{`{"config":{"creditUsagePercent":0,"prepaidBalance":{"val":"NaN"}}}`, false, false},
		{`{"config":{"monthlyLimit":{},"used":0}}`, true, false},
		{`{"config":{"monthlyLimit":{"val":null},"used":0}}`, true, false},
		{`{"config":{"monthlyLimit":100}}`, true, false},
		{`{"config":{"monthlyLimit":100,"used":0},"error":"failed"}`, true, false},
		{`{"config":{"creditUsagePercent":0,"creditUsagePercent":10}}`, false, false},
		{`{"config":{"creditUsagePercent":0}} {}`, false, false},
	} {
		err := validateGrokBillingWindow([]byte(tc.body), tc.monthly)
		if (err == nil) != tc.valid {
			t.Fatalf("monthly=%v payload=%s: %v", tc.monthly, tc.body, err)
		}
		if tc.valid {
			items, err := parseSupplierQuota("grok", []byte(tc.body))
			if err != nil || len(items) != 1 || items[0].Value != strings.TrimSuffix(strings.TrimPrefix(tc.body, `{"config":`), "}") {
				t.Fatal("raw billing data was changed", items, err)
			}
		}
	}
}

func TestGrokBillingAuthRecoveryAndIsolation(t *testing.T) {
	p := newGrokQuotaTestProvider(t)
	adapter := &quotaRefreshAdapter{service: oauth.OAuthServiceGrok}
	if err := p.manager.Register(adapter); err != nil {
		t.Fatal(err)
	}
	calls := 0
	p.client = &http.Client{Transport: quotaTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		status, body := http.StatusOK, grokWeeklyFixture
		if calls == 1 {
			status, body = http.StatusUnauthorized, `{}`
		} else if r.Header.Get("Authorization") != "Bearer renewed" {
			t.Fatal("renewed token was not reused")
		}
		if r.URL.RawQuery == "" {
			body = grokMonthlyFixture
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	if _, err := p.FetchQuota(t.Context()); err != nil || calls != 3 || adapter.calls != 1 {
		t.Fatal(err, calls, adapter.calls)
	}
	p.config.APIEndpoint = "https://custom.invalid/v1"
	if _, err := p.FetchQuota(t.Context()); !errors.Is(err, ErrQuotaNotConfigured) || calls != 3 {
		t.Fatal("sent custom upstream credentials to official billing", err)
	}
	p.config.APIEndpoint = ""
	p.config.Kind = "api"
	if _, err := p.FetchQuota(t.Context()); !errors.Is(err, ErrQuotaUnsupported) || calls != 3 {
		t.Fatal("API account reached subscription billing", err)
	}
}

func TestGrokBillingLateResponseCannotRestoreInvalidatedCache(t *testing.T) {
	p := newGrokQuotaTestProvider(t)
	p.client = &http.Client{Transport: quotaTransport(func(r *http.Request) (*http.Response, error) {
		body := grokWeeklyFixture
		if r.URL.RawQuery == "" {
			p.quotaCache.invalidate()
			body = grokMonthlyFixture
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	if _, err := p.FetchQuota(t.Context()); !errors.Is(err, ErrQuotaSuperseded) || p.GetCachedQuota().CacheStatus != QuotaCacheMissing {
		t.Fatal("obsolete billing snapshot was cached", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.FetchQuota(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestSubscriptionResponseHeadersRefreshQuotaThroughHandle(t *testing.T) {
	for _, tc := range []struct {
		service, header, value string
	}{
		{"claude", "anthropic-ratelimit-unified-7d_oi-utilization", "0.2500"},
		{"codex", "x-codex-secondary-reset-after-seconds", "3600"},
		{"grok", "x-ratelimit-remaining-tokens", "0"},
	} {
		for _, status := range []int{http.StatusOK, http.StatusTooManyRequests, http.StatusForbidden, http.StatusBadGateway} {
			t.Run(tc.service+http.StatusText(status), func(t *testing.T) {
				p := &oauthAIProvider{
					manager: oauth.NewOAuthManager(&quotaCredentialStore{raw: []byte(`{"access_token":"access"}`)}),
					config:  AIProviderConfig{Kind: "subscription", AuthType: AuthTypeOAuth, CredentialID: "cred"},
				}
				old := p.quotaCache.begin()
				old.observed = time.Now().Add(-2 * quotaCacheTTL)
				p.quotaCache.putSubscription(old, []subscriptionUpdate{{key: "prior-window", item: SubscriptionQuotaItem{TimeDimension: "prior-window", Usage: 12}, hasUsage: true}}, false)
				before := p.GetCachedQuota()
				const stream = "data: {\"usage\":{\"input_tokens\":7,\"output_tokens\":3}}\n\ndata: [DONE]\n\n"
				p.client = &http.Client{Transport: quotaTransport(func(*http.Request) (*http.Response, error) {
					h := make(http.Header)
					h.Set(tc.header, tc.value)
					if tc.service == "codex" {
						h.Set("x-codex-secondary-used-percent", "25")
					}
					if tc.service == "grok" {
						h.Set("x-ratelimit-limit-tokens", "100")
					}
					h.Set("Content-Type", "text/event-stream")
					return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(stream))}, nil
				})}
				call := func() {
					w := httptest.NewRecorder()
					r := httptest.NewRequest(http.MethodPost, "https://upstream.invalid/v1/responses", strings.NewReader(`{"model":"test"}`))
					r = r.WithContext(WithResponseWriter(t.Context(), w))
					p.handle(tc.service, "cred", r, nil)
					if w.Body.String() != stream || w.Code != status || w.Header().Get(tc.header) != tc.value {
						t.Fatal("quota collection changed the streamed response")
					}
				}
				call()
				got := p.GetCachedQuota()
				if status == http.StatusOK || status == http.StatusTooManyRequests {
					want := 25.0
					if tc.service == "grok" {
						want = 100
					}
					if len(got.Items) != 0 || len(got.Subscription) != 2 || got.Subscription[1].Usage != want || got.CacheStatus != QuotaCacheStale || !got.UpdatedAt.After(before.UpdatedAt) {
						t.Fatalf("passive update lost old fields or made them fresh: %+v", got)
					}
				} else if !reflect.DeepEqual(before, got) {
					t.Fatal("error response poisoned quota cache")
				}
				p.config.Kind = "api"
				p.quotaCache.invalidate()
				call()
				if p.GetCachedQuota().CacheStatus != QuotaCacheMissing {
					t.Fatal("API rate-limit headers became account balance")
				}
			})
		}
	}
}
