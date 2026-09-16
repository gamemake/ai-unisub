package aiprovider

import (
	"net/http"
	"reflect"
	"testing"
	"time"
)

func TestSubscriptionBodyNormalization(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, supplier, body string
		want                 []SubscriptionQuotaItem
	}{
		{"claude", "anthropic", `{"five_hour":{"utilization":0,"resets_at":"2030-01-01T08:00:00+08:00"},"seven_day":{"utilization":125},"seven_day_sonnet":{"utilization":10},"seven_day_overage_included":null}`, []SubscriptionQuotaItem{{"5h", 0, now}, {"weekly", 125, time.Time{}}, {"weekly (sonnet)", 10, time.Time{}}}},
		{"codex", "openai", `{"rate_limit":{"primary_window":{"used_percent":25,"limit_window_seconds":604800,"reset_after_seconds":3600},"secondary_window":{"used_percent":0,"limit_window_seconds":18000,"reset_at":1893456000}},"additional_rate_limits":[{"limit_name":"spark","rate_limit":{"primary_window":{"used_percent":50,"limit_window_seconds":18000}}}]}`, []SubscriptionQuotaItem{{"weekly", 25, now.Add(time.Hour)}, {"5h", 0, now}, {"5h (spark)", 50, time.Time{}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := parseSupplierQuota(tc.supplier, []byte(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			updates, err := subscriptionBodyUpdates(tc.supplier, raw, now)
			if err != nil {
				t.Fatal(err)
			}
			var got []SubscriptionQuotaItem
			for _, u := range updates {
				got = append(got, u.item)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v; want %+v", got, tc.want)
			}
		})
	}
}

func TestSubscriptionHeadersMergeWithBody(t *testing.T) {
	var cache quotaCache
	old := cache.begin()
	old.observed = time.Now().UTC().Add(-2 * quotaCacheTTL)
	body, err := parseSupplierQuota("openai", []byte(`{"rate_limit":{"primary_window":{"used_percent":20,"limit_window_seconds":18000,"reset_at":1893456000},"secondary_window":{"used_percent":30,"limit_window_seconds":604800}}}`))
	if err != nil {
		t.Fatal(err)
	}
	updates, err := subscriptionBodyUpdates("openai", body, old.observed)
	if err != nil {
		t.Fatal(err)
	}
	cache.putSubscription(old, updates, false)
	stamp := cache.begin()
	h := http.Header{}
	h.Set("x-codex-primary-used-percent", "0")
	h.Set("x-codex-primary-reset-after-seconds", "3600")
	updates = subscriptionHeaderUpdates(AIProviderConfig{Kind: "subscription"}, "codex", h, stamp.observed)
	cache.putSubscription(stamp, updates, true)
	got := cache.get()
	if len(got.Items) != 0 || len(got.Subscription) != 2 || got.Subscription[0].TimeDimension != "5h" || got.Subscription[0].Usage != 0 || got.Subscription[0].ResetAt.Unix() != stamp.observed.Unix()+3600 || got.Subscription[1].Usage != 30 || got.CacheStatus != QuotaCacheStale {
		t.Fatal("header did not merge into body window without refreshing the other window", got)
	}
	copy := cache.get()
	copy.Subscription[0].Usage = 999
	cache.putSubscription(old, []subscriptionUpdate{{key: "primary", item: SubscriptionQuotaItem{Usage: 90}, hasUsage: true}}, true)
	if !reflect.DeepEqual(cache.get(), got) {
		t.Fatal("alias or older query changed cache")
	}
	cache.invalidate()
	if cache.putSubscription(stamp, updates, true) || cache.get().CacheStatus != QuotaCacheMissing {
		t.Fatal("old config restored cache")
	}
}

func TestNormalizedHeadersAndUnknownFields(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		service string
		headers map[string]string
		usage   float64
		reset   time.Time
	}{
		{"claude", map[string]string{"anthropic-ratelimit-unified-5h-utilization": "0.25", "anthropic-ratelimit-unified-5h-reset": "1893456000000"}, 25, now},
		{"codex", map[string]string{"x-codex-primary-used-percent": "25", "x-codex-primary-reset-after-seconds": "60"}, 25, now.Add(time.Minute)},
		{"grok", map[string]string{"x-ratelimit-limit-tokens": "1000", "x-ratelimit-remaining-tokens": "750", "x-ratelimit-reset-tokens": "1m30s"}, 25, now.Add(90 * time.Second)},
		{"grok", map[string]string{"x-rate-limit-limit-requests": "100", "x-rate-limit-remaining-requests": "0", "x-rate-limit-reset-requests": "60"}, 100, now.Add(time.Minute)},
	} {
		t.Run(tc.service+tc.reset.String(), func(t *testing.T) {
			h := http.Header{}
			for k, v := range tc.headers {
				h.Set(k, v)
			}
			u := subscriptionHeaderUpdates(AIProviderConfig{Kind: "subscription"}, tc.service, h, now)
			if len(u) != 1 || !u[0].hasUsage || u[0].item.Usage != tc.usage || !u[0].item.ResetAt.Equal(tc.reset) {
				t.Fatal(u)
			}
			if got := subscriptionHeaderUpdates(AIProviderConfig{Kind: "api"}, tc.service, h, now); len(got) != 0 {
				t.Fatal("API returned subscription", got)
			}
		})
	}
	var cache quotaCache
	stamp := cache.begin()
	h := http.Header{}
	h.Set("anthropic-ratelimit-unified-5h-reset", "1893456000")
	cache.putSubscription(stamp, subscriptionHeaderUpdates(AIProviderConfig{Kind: "subscription"}, "claude", h, stamp.observed), true)
	if cache.get().CacheStatus != QuotaCacheMissing {
		t.Fatal("missing usage was treated as zero")
	}
	h.Del("anthropic-ratelimit-unified-5h-reset")
	h.Set("anthropic-ratelimit-unified-5h-utilization", "0")
	stamp = cache.begin()
	cache.putSubscription(stamp, subscriptionHeaderUpdates(AIProviderConfig{Kind: "subscription"}, "claude", h, stamp.observed), true)
	if got := cache.get(); len(got.Subscription) != 1 || got.Subscription[0].Usage != 0 || got.Subscription[0].ResetAt.Unix() != 1893456000 {
		t.Fatal(got)
	}
}

func TestGrokZeroLimitAndNonfiniteUsage(t *testing.T) {
	items := []QuotaItem{
		{Name: "config", Source: "billing?format=credits", Value: `{"creditUsagePercent":0}`},
		{Name: "config", Source: "billing", Value: `{"monthlyLimit":0,"used":0}`},
	}
	u, err := subscriptionBodyUpdates("grok", items, time.Now())
	if err != nil || len(u) != 1 || u[0].item.TimeDimension != "weekly" {
		t.Fatal(u, err)
	}
	if _, err = subscriptionBodyUpdates("anthropic", []QuotaItem{{Name: "five_hour", Value: `{"utilization":1e999}`}}, time.Now()); err == nil {
		t.Fatal("nonfinite usage accepted")
	}
}
