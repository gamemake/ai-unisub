package aiprovider

import (
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
