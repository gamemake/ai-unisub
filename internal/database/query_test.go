package database

import (
	"testing"
	"time"
)

func testDB(t *testing.T) Database {
	t.Helper()
	db, err := NewDatabase("sqlite::memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestQueryCallTracesRequiresTimeRange(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	if _, _, err := db.QueryCallTraces(CallTraceFilter{}, 1, 10); err == nil {
		t.Fatal("empty time range was accepted")
	}
	if _, _, err := db.QueryCallTraces(CallTraceFilter{TimeRange: TimeRange{Start: now}}, 1, 10); err == nil {
		t.Fatal("missing end time was accepted")
	}
	if _, _, err := db.QueryCallTraces(CallTraceFilter{TimeRange: TimeRange{Start: now, End: now.Add(-time.Hour)}}, 1, 10); err == nil {
		t.Fatal("start after end was accepted")
	}
	if _, _, err := db.QueryCallTraces(CallTraceFilter{TimeRange: TimeRange{Start: now.Add(-time.Hour), End: now}}, 1, 10); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-400 * 24 * time.Hour)
	if _, _, err := db.QueryCallTraces(CallTraceFilter{TimeRange: TimeRange{Start: old, End: old.Add(24 * time.Hour)}}, 1, 10); err != nil {
		t.Fatal(err)
	}
}

func TestQueryProxyLogsRequiresTimeRange(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	if _, _, err := db.QueryProxyLogs(ProxyLogFilter{GroupID: 1}, 1, 10); err == nil {
		t.Fatal("empty time range was accepted")
	}
	if _, _, err := db.QueryProxyLogs(ProxyLogFilter{GroupID: 1, TimeRange: TimeRange{Start: now.Add(-time.Hour), End: now}}, 1, 10); err != nil {
		t.Fatal(err)
	}
}
