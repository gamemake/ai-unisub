package aiprovider

import (
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
