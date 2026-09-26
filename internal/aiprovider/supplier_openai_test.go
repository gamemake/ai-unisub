package aiprovider

import (
	"net/http"
	"testing"
	"time"
)

func TestOpenAIParseSubscriptionQuota(t *testing.T) {
	body := []byte(`{
		"plan_type": "plus",
		"rate_limit": {
			"allowed": true, "limit_reached": false,
			"primary_window": {"used_percent": 12.5, "limit_window_seconds": 18000, "reset_after_seconds": 600, "reset_at": 0},
			"secondary_window": {"used_percent": 40, "limit_window_seconds": 604800, "reset_after_seconds": 3600, "reset_at": 1790000000}
		},
		"additional_rate_limits": [
			{"limit_name": "spark", "metered_feature": "codex_spark", "rate_limit": {"primary_window": {"used_percent": 0, "limit_window_seconds": 604800, "reset_at": 1790000000}, "secondary_window": null}},
			{"limit_name": "empty", "rate_limit": null}
		]
	}`)
	observedAt := time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC)
	windows, err := (&SupplierOpenAI{}).parseSubscriptionQuota(body, observedAt)
	if err != nil {
		t.Fatal(err)
	}
	want := []SubscriptionQuotaItem{
		{TimeDimension: "5h", Usage: 12.5, ResetAt: observedAt.Add(600 * time.Second)},
		{TimeDimension: "weekly", Usage: 40, ResetAt: time.Unix(1790000000, 0).UTC()},
		{TimeDimension: "weekly_spark", Usage: 0, ResetAt: time.Unix(1790000000, 0).UTC()},
	}
	if len(windows) != len(want) {
		t.Fatalf("windows = %+v", windows)
	}
	for i := range want {
		if windows[i].TimeDimension != want[i].TimeDimension || windows[i].Usage != want[i].Usage || !windows[i].ResetAt.Equal(want[i].ResetAt) {
			t.Fatalf("window %d = %+v, want %+v", i, windows[i], want[i])
		}
	}

	if _, err := (&SupplierOpenAI{}).parseSubscriptionQuota([]byte(`{"rate_limit": null}`), observedAt); err == nil {
		t.Fatal("expected error when no window has usage")
	}
}

func TestCodexWindowDimension(t *testing.T) {
	for seconds, want := range map[int64]string{18000: "5h", 604800: "weekly", 86400: "1d", 2592000: "monthly", 5400: "90m", 0: "unknown"} {
		if got := codexWindowDimension(seconds); got != want {
			t.Errorf("codexWindowDimension(%d) = %q, want %q", seconds, got, want)
		}
	}
}

func TestOpenAIPostResponseMergesCodexHeaders(t *testing.T) {
	account := &Account{
		Config: AccountConfig{Kind: AccountSubscription},
		Quota: AccountQuota{Subscription: []SubscriptionQuotaItem{
			{TimeDimension: "5h", Usage: 1},
			{TimeDimension: "weekly_spark", Usage: 2},
		}},
	}
	header := http.Header{}
	header.Set("X-Codex-Primary-Used-Percent", "12.5")
	header.Set("X-Codex-Primary-Window-Minutes", "300")
	header.Set("X-Codex-Primary-Reset-After-Seconds", "600")
	header.Set("X-Codex-Secondary-Used-Percent", "40")
	header.Set("X-Codex-Secondary-Window-Minutes", "10080")
	header.Set("X-Codex-Primary-Over-Secondary-Limit-Percent", "30")

	before := time.Now().UTC()
	if err := (&SupplierOpenAI{}).PostResponse(account, &http.Response{StatusCode: http.StatusTooManyRequests, Header: header}, nil); err != nil {
		t.Fatal(err)
	}
	got := account.Quota.Subscription
	if len(got) != 3 || got[0].TimeDimension != "5h" || got[0].Usage != 12.5 ||
		got[1].TimeDimension != "weekly_spark" || got[1].Usage != 2 ||
		got[2].TimeDimension != "weekly" || got[2].Usage != 40 || !got[2].ResetAt.IsZero() {
		t.Fatalf("windows = %+v", got)
	}
	if reset := got[0].ResetAt.Sub(before); reset < 600*time.Second || reset > 610*time.Second {
		t.Fatalf("5h reset = %v", got[0].ResetAt)
	}
}

func TestOpenAIParseSubscriptionHeadersSkipsUnnamedWindow(t *testing.T) {
	header := http.Header{}
	header.Set("X-Codex-Primary-Used-Percent", "12.5")
	header.Set("X-Codex-Secondary-Used-Percent", "bad")
	header.Set("X-Codex-Secondary-Window-Minutes", "10080")
	if windows := (&SupplierOpenAI{}).parseSubscriptionHeaders(header, time.Now()); len(windows) != 0 {
		t.Fatalf("windows = %+v", windows)
	}
}
