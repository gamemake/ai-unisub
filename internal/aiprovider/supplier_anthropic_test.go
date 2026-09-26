package aiprovider

import (
	"net/http"
	"testing"
	"time"
)

func TestAnthropicParseSubscriptionQuota(t *testing.T) {
	body := []byte(`{
		"five_hour": {"utilization": 2.0, "resets_at": "2026-09-26T11:40:00.242299+00:00", "limit_dollars": null},
		"seven_day": {"utilization": 8.0, "resets_at": "2026-09-28T08:00:00.242321+00:00"},
		"seven_day_opus": null,
		"seven_day_sonnet": {"utilization": 0.0, "resets_at": null},
		"nimbus_quill": {"utilization": 0.0, "resets_at": null},
		"extra_usage": {"is_enabled": false, "utilization": null},
		"limits": [{"kind": "session", "percent": 2}]
	}`)
	windows, err := (&SupplierAnthropic{}).parseSubscriptionQuota(body)
	if err != nil {
		t.Fatal(err)
	}
	want := []SubscriptionQuotaItem{
		{TimeDimension: "5h", Usage: 2, ResetAt: time.Date(2026, 9, 26, 11, 40, 0, 242299000, time.UTC)},
		{TimeDimension: "weekly", Usage: 8, ResetAt: time.Date(2026, 9, 28, 8, 0, 0, 242321000, time.UTC)},
		{TimeDimension: "weekly_sonnet", Usage: 0},
	}
	if len(windows) != len(want) {
		t.Fatalf("windows = %+v", windows)
	}
	for i := range want {
		if windows[i].TimeDimension != want[i].TimeDimension || windows[i].Usage != want[i].Usage || !windows[i].ResetAt.Equal(want[i].ResetAt) {
			t.Fatalf("window %d = %+v, want %+v", i, windows[i], want[i])
		}
	}

	if _, err := (&SupplierAnthropic{}).parseSubscriptionQuota([]byte(`{"five_hour": null}`)); err == nil {
		t.Fatal("expected error when no window has utilization")
	}
}

func TestAnthropicPostResponseMergesUnifiedHeaders(t *testing.T) {
	cachedReset := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	account := &Account{
		Config: AccountConfig{Kind: AccountSubscription},
		Quota: AccountQuota{Subscription: []SubscriptionQuotaItem{
			{TimeDimension: "5h", Usage: 1},
			{TimeDimension: "weekly", Usage: 2, ResetAt: cachedReset},
			{TimeDimension: "weekly_sonnet", Usage: 3},
		}},
	}
	header := http.Header{}
	header.Set("Anthropic-Ratelimit-Unified-5h-Utilization", "0.25")
	header.Set("Anthropic-Ratelimit-Unified-5h-Reset", "1790000000")
	header.Set("Anthropic-Ratelimit-Unified-7d-Utilization", "0.5")
	header.Set("Anthropic-Ratelimit-Unified-7d_oi-Utilization", "0.1")
	header.Set("Anthropic-Ratelimit-Unified-7d_oi-Reset", "1790000000000")
	header.Set("Anthropic-Ratelimit-Unified-Status", "allowed")
	header.Set("Anthropic-Ratelimit-Unified-Fallback-Utilization", "0.9")
	header.Add("Anthropic-Ratelimit-Unified-7d_opus-Utilization", "0.1")
	header.Add("Anthropic-Ratelimit-Unified-7d_opus-Utilization", "0.2")

	if err := (&SupplierAnthropic{}).PostResponse(account, &http.Response{StatusCode: http.StatusOK, Header: header}, nil); err != nil {
		t.Fatal(err)
	}
	want := []SubscriptionQuotaItem{
		{TimeDimension: "5h", Usage: 25, ResetAt: time.Unix(1790000000, 0).UTC()},
		{TimeDimension: "weekly", Usage: 50, ResetAt: cachedReset},
		{TimeDimension: "weekly_sonnet", Usage: 3},
		{TimeDimension: "weekly_overage_included", Usage: 10, ResetAt: time.Unix(1790000000, 0).UTC()},
	}
	assertSubscriptionWindows(t, account.Quota.Subscription, want)
	if account.Quota.CacheStatus != QuotaCacheFresh || account.Quota.UpdatedAt.IsZero() || !account.Dirty {
		t.Fatalf("quota = %+v, dirty = %v", account.Quota, account.Dirty)
	}
}

func TestAnthropicPostResponseIgnoresFailedResponses(t *testing.T) {
	account := &Account{Config: AccountConfig{Kind: AccountSubscription}}
	header := http.Header{}
	header.Set("Anthropic-Ratelimit-Unified-5h-Utilization", "0.25")
	if err := (&SupplierAnthropic{}).PostResponse(account, &http.Response{StatusCode: http.StatusInternalServerError, Header: header}, nil); err != nil {
		t.Fatal(err)
	}
	if len(account.Quota.Subscription) != 0 || account.Dirty {
		t.Fatalf("quota = %+v, dirty = %v", account.Quota, account.Dirty)
	}
}

func assertSubscriptionWindows(t *testing.T, got, want []SubscriptionQuotaItem) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("windows = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].TimeDimension != want[i].TimeDimension || got[i].Usage != want[i].Usage || !got[i].ResetAt.Equal(want[i].ResetAt) {
			t.Fatalf("window %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
