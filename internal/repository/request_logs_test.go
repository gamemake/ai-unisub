package repository

import (
	"context"
	"testing"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
)

func TestListRequestLogsFiltersByAccountIDs(t *testing.T) {
	repo := testRepository(t)
	ctx := context.Background()
	ownerA, err := repo.CreateUser(ctx, "owner-a", "owner-a-pass-ok", model.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	ownerB, err := repo.CreateUser(ctx, "owner-b", "owner-b-pass-ok", model.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	accountA, err := repo.CreateAccount(ctx, CreateAccountParams{
		Name: "account-a", Provider: model.ProviderCodex, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "token-a"}, CreatedByUserID: &ownerA.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	accountB, err := repo.CreateAccount(ctx, CreateAccountParams{
		Name: "account-b", Provider: model.ProviderCodex, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "token-b"}, CreatedByUserID: &ownerB.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	keyA, _, err := repo.CreateAPIKey(ctx, CreateAPIKeyParams{AccountID: accountA.ID, Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	keyB, _, err := repo.CreateAPIKey(ctx, CreateAPIKeyParams{AccountID: accountB.ID, Name: "b"})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 8, 21, 8, 0, 0, 0, time.UTC)
	for i, item := range []struct {
		accountID int64
		keyID     int64
	}{{accountA.ID, keyA.ID}, {accountB.ID, keyB.ID}} {
		accountID, apiKeyID := item.accountID, item.keyID
		if err := repo.RecordRequest(time.UTC, RequestLog{
			AccountID: &accountID, APIKeyID: &apiKeyID, Provider: "codex", Method: "POST", Path: "/v1/responses",
			StatusCode: 200, StartedAt: started.Add(time.Duration(i) * time.Second), FinishedAt: started.Add(time.Duration(i)*time.Second + time.Millisecond),
			Model: "gpt-test",
		}); err != nil {
			t.Fatal(err)
		}
	}
	owned, err := repo.ListAccountIDsByCreator(ctx, ownerA.ID)
	if err != nil {
		t.Fatal(err)
	}
	logs, total, err := repo.ListRequestLogs(ctx, time.UTC, RequestLogFilter{AccountIDs: owned, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(logs) != 1 || logs[0].AccountID == nil || *logs[0].AccountID != accountA.ID {
		t.Fatalf("owner-a logs = total:%d data:%+v", total, logs)
	}
}

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
		Query: "beta=1", ClientIP: "203.0.113.10", StatusCode: 200, StartedAt: started, FinishedAt: started.Add(40 * time.Millisecond),
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
	if summary.AccountName != "codex-main" || summary.APIKeyName != "bot" || summary.Model != "gpt-test" || summary.ClientIP != "203.0.113.10" {
		t.Fatalf("summary = %+v", summary)
	}
	if summary.InputTokens == nil || *summary.InputTokens != 12 || summary.RequestBody != "" {
		t.Fatalf("list should omit bodies and keep tokens: %+v", summary)
	}
	detail, err := repo.GetRequestLog(ctx, "20260820", summary.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.RequestBody != `{"model":"gpt-test"}` || detail.Query != "beta=1" || detail.ClientIP != "203.0.113.10" || detail.ResponseBody == "" {
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
	updatedAccount, err := repo.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updatedAccount.LastUsedAt == nil || !updatedAccount.LastUsedAt.Equal(started.Add(40*time.Millisecond)) {
		t.Fatalf("account last_used_at = %v", updatedAccount.LastUsedAt)
	}
}

func TestUsageSummaryAggregatesRequestLogTokens(t *testing.T) {
	repo := testRepository(t)
	ctx := context.Background()
	account, err := repo.CreateAccount(ctx, CreateAccountParams{
		Name: "usage-account", Provider: model.ProviderClaude, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	accountID := account.ID
	input, output, cacheRead, cacheCreate, total := int64(10), int64(4), int64(3), int64(2), int64(14)
	olderInput, olderOutput := int64(100), int64(50)
	now := time.Now().UTC()
	if err := repo.RecordRequest(time.Local, RequestLog{
		AccountID: &accountID, Provider: "claude", Method: "POST", Path: "/v1/messages",
		StatusCode: 200, StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-time.Hour + time.Second),
		InputTokens: &input, OutputTokens: &output, CacheReadTokens: &cacheRead, CacheCreationTokens: &cacheCreate, TotalTokens: &total,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordRequest(time.Local, RequestLog{
		AccountID: &accountID, Provider: "claude", Method: "POST", Path: "/v1/messages",
		StatusCode: 200, StartedAt: now.Add(-30 * time.Hour), FinishedAt: now.Add(-30*time.Hour + time.Second),
		InputTokens: &olderInput, OutputTokens: &olderOutput,
	}); err != nil {
		t.Fatal(err)
	}
	summary, err := repo.UsageSummary(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Requests24H != 1 || summary.InputTokens24H != 10 || summary.OutputTokens24H != 4 {
		t.Fatalf("usage summary core = %+v", summary)
	}
	if summary.CacheReadTokens24H != 3 || summary.CacheCreationTokens24H != 2 || summary.TotalTokens24H != 14 {
		t.Fatalf("usage summary tokens = %+v", summary)
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
		exists, err := repo.db.TableExists(context.Background(), table)
		if err != nil || !exists {
			t.Fatalf("table %s was not retained: exists=%v err=%v", table, exists, err)
		}
	}
}
