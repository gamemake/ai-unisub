package repository

import (
	"context"
	"testing"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
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

func TestRecordRequestStoresHTTPAndTokens(t *testing.T) {
	repo := testRepository(t)
	ctx := context.Background()
	account, err := repo.CreateAccount(ctx, CreateAccountParams{
		Name: "codex-main", Provider: model.ProviderCodex, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := repo.CreateAPIKey(ctx, CreateAPIKeyParams{AccountID: account.ID, Name: "bot"})
	if err != nil {
		t.Fatal(err)
	}
	accountID, apiKeyID := account.ID, key.ID
	input, output, total := int64(12), int64(34), int64(46)
	started := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	err = repo.RecordRequest(time.UTC, RequestLog{
		AccountID: &accountID, APIKeyID: &apiKeyID, Provider: "codex", Method: "POST", Path: "/v1/responses",
		Query: "beta=1", StatusCode: 200, StartedAt: started, FinishedAt: started.Add(40 * time.Millisecond),
		RequestID: "req_1", Model: "gpt-test", InputTokens: &input, OutputTokens: &output, TotalTokens: &total,
		RequestHeaders:  `{"Content-Type":["application/json"],"Authorization":["[redacted]"]}`,
		RequestBody:     `{"model":"gpt-test"}`,
		ResponseHeaders: `{"Content-Type":["application/json"]}`,
		ResponseBody:    `{"id":"r1","usage":{"input_tokens":12,"output_tokens":34}}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	logs, totalCount, err := repo.ListRequestLogs(ctx, time.UTC, RequestLogFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if totalCount != 1 || len(logs) != 1 {
		t.Fatalf("list count=%d len=%d", totalCount, len(logs))
	}
	summary := logs[0]
	if summary.AccountName != "codex-main" || summary.APIKeyName != "bot" || summary.Model != "gpt-test" {
		t.Fatalf("summary = %+v", summary)
	}
	if summary.InputTokens == nil || *summary.InputTokens != 12 || summary.RequestBody != "" {
		t.Fatalf("list should omit bodies and keep tokens: %+v", summary)
	}
	detail, err := repo.GetRequestLog(ctx, "20260820", summary.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.RequestBody != `{"model":"gpt-test"}` || detail.Query != "beta=1" || detail.ResponseBody == "" {
		t.Fatalf("detail http = %+v", detail)
	}
	if detail.RequestHeaders != `{"Content-Type":["application/json"],"Authorization":["[redacted]"]}` {
		t.Fatalf("request headers = %s", detail.RequestHeaders)
	}
	if detail.ResponseHeaders != `{"Content-Type":["application/json"]}` {
		t.Fatalf("response headers = %s", detail.ResponseHeaders)
	}
	if detail.TotalTokens == nil || *detail.TotalTokens != 46 {
		t.Fatalf("detail tokens = %+v", detail)
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
