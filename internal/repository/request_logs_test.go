package repository

import (
	"context"
	"testing"
	"time"
)

func TestRecordRequestUsesConfiguredTimezoneDailyTable(t *testing.T) {
	repo := testRepository(t)
	location, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatal(err)
	}
	accountID, apiKeyID := int64(11), int64(22)
	started := time.Date(2026, 8, 18, 16, 30, 0, 0, time.UTC) // 2026-08-19 in Taipei.
	err = repo.RecordRequest(location, RequestLog{
		AccountID: &accountID, APIKeyID: &apiKeyID, Provider: "codex", Method: "POST", Path: "/v1/responses",
		StatusCode: 429, StartedAt: started, FinishedAt: started.Add(1500 * time.Millisecond), ErrorType: "concurrency_limited",
	})
	if err != nil {
		t.Fatal(err)
	}
	var count, status, duration int
	var errorType string
	if err := repo.db.QueryRow(`SELECT COUNT(*), status_code, duration_ms, error_type FROM request_logs_20260819`).Scan(&count, &status, &duration, &errorType); err != nil {
		t.Fatal(err)
	}
	if count != 1 || status != 429 || duration != 1500 || errorType != "concurrency_limited" {
		t.Fatalf("request log = count:%d status:%d duration:%d error:%q", count, status, duration, errorType)
	}
}

func TestCleanupRequestLogTablesKeepsConfiguredCalendarDays(t *testing.T) {
	repo := testRepository(t)
	location, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatal(err)
	}
	for _, date := range []string{"20260720", "20260721", "20260819"} {
		at, err := time.ParseInLocation("20060102", date, location)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.RecordRequest(location, RequestLog{Method: "GET", Path: "/v1/models", StatusCode: 200, StartedAt: at, FinishedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.db.Exec(`CREATE TABLE request_logs_invalid (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, location)
	dropped, err := repo.CleanupRequestLogTables(context.Background(), now, location, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(dropped) != 1 || dropped[0] != "request_logs_20260720" {
		t.Fatalf("dropped tables = %v", dropped)
	}
	for _, table := range []string{"request_logs_20260721", "request_logs_20260819", "request_logs_invalid"} {
		var count int
		if err := repo.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("table %s was not retained: count=%d err=%v", table, count, err)
		}
	}
}
