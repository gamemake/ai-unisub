package repository

import (
	"context"
	"testing"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
)

func TestListRequestLogsFiltersBySubscriptionID(t *testing.T) {
	repo := testRepository(t)
	ctx := context.Background()
	accountA, err := repo.CreateSubscription(ctx, CreateSubscriptionParams{
		Name: "account-a", Provider: model.ProviderCodex, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "token-a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	accountB, err := repo.CreateSubscription(ctx, CreateSubscriptionParams{
		Name: "account-b", Provider: model.ProviderCodex, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "token-b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	keyA, _, err := repo.CreateAPIKey(ctx, CreateAPIKeyParams{SubscriptionID: accountA.ID, Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	keyB, _, err := repo.CreateAPIKey(ctx, CreateAPIKeyParams{SubscriptionID: accountB.ID, Name: "b"})
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
			SubscriptionID: &accountID, APIKeyID: &apiKeyID, Provider: "codex", Method: "POST", Path: "/v1/responses",
			StatusCode: 200, StartedAt: started.Add(time.Duration(i) * time.Second), FinishedAt: started.Add(time.Duration(i)*time.Second + time.Millisecond),
			Model: "gpt-test",
		}); err != nil {
			t.Fatal(err)
		}
	}
	filterID := accountA.ID
	logs, total, err := repo.ListRequestLogs(ctx, time.UTC, RequestLogFilter{SubscriptionID: &filterID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(logs) != 1 || logs[0].SubscriptionID == nil || *logs[0].SubscriptionID != accountA.ID {
		t.Fatalf("filtered logs = total:%d data:%+v", total, logs)
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
		SubscriptionID: &accountID, APIKeyID: &apiKeyID, Provider: "codex", Method: "POST", Path: "/v1/responses",
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
	account, err := repo.CreateSubscription(ctx, CreateSubscriptionParams{
		Name: "codex-main", Provider: model.ProviderCodex, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := repo.CreateAPIKey(ctx, CreateAPIKeyParams{SubscriptionID: account.ID, Name: "bot"})
	if err != nil {
		t.Fatal(err)
	}
	accountID, apiKeyID := account.ID, key.ID
	input, output, total := int64(12), int64(34), int64(46)
	started := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	err = repo.RecordRequest(time.UTC, RequestLog{
		SubscriptionID: &accountID, APIKeyID: &apiKeyID, Provider: "codex", Method: "POST", Path: "/v1/responses",
		Query: "beta=1", ClientIP: "203.0.113.10", StatusCode: 200, StartedAt: started, FinishedAt: started.Add(40 * time.Millisecond),
		RequestID: "req_1", Model: "gpt-test", InputTokens: &input, OutputTokens: &output, TotalTokens: &total,
		RequestHeaders:  `{"Authorization":["Bearer unisub_test"],"Content-Type":["application/json"]}`,
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
	if summary.SubscriptionName != "codex-main" || summary.APIKeyName != "bot" || summary.Model != "gpt-test" || summary.ClientIP != "203.0.113.10" {
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
	if detail.RequestHeaders != `{"Authorization":["Bearer unisub_test"],"Content-Type":["application/json"]}` {
		t.Fatalf("request headers = %s", detail.RequestHeaders)
	}
	if detail.ResponseHeaders != `{"Content-Type":["application/json"]}` {
		t.Fatalf("response headers = %s", detail.ResponseHeaders)
	}
	if detail.TotalTokens == nil || *detail.TotalTokens != 46 {
		t.Fatalf("detail tokens = %+v", detail)
	}
	updatedAccount, err := repo.GetSubscription(ctx, account.ID)
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
	account, err := repo.CreateSubscription(ctx, CreateSubscriptionParams{
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
		SubscriptionID: &accountID, Provider: "claude", Method: "POST", Path: "/v1/messages",
		StatusCode: 200, StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-time.Hour + time.Second),
		InputTokens: &input, OutputTokens: &output, CacheReadTokens: &cacheRead, CacheCreationTokens: &cacheCreate, TotalTokens: &total,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordRequest(time.Local, RequestLog{
		SubscriptionID: &accountID, Provider: "claude", Method: "POST", Path: "/v1/messages",
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

func TestListRequestLogsSearchesByUsername(t *testing.T) {
	repo := testRepository(t)
	ctx := context.Background()
	alice, err := repo.CreateUser(ctx, "alice", "alice-password-ok", model.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := repo.CreateUser(ctx, "bob", "bob-password-ok", model.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	subscription, err := repo.CreateSubscription(ctx, CreateSubscriptionParams{
		Name: "shared", Provider: model.ProviderCodex, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	aliceKey, _, err := repo.CreateAPIKey(ctx, CreateAPIKeyParams{SubscriptionID: subscription.ID, UserID: &alice.ID, Name: "alice-key"})
	if err != nil {
		t.Fatal(err)
	}
	bobKey, _, err := repo.CreateAPIKey(ctx, CreateAPIKeyParams{SubscriptionID: subscription.ID, UserID: &bob.ID, Name: "bob-key"})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 9, 8, 8, 0, 0, 0, time.UTC)
	for i, item := range []struct {
		keyID  int64
		userID int64
	}{{aliceKey.ID, alice.ID}, {bobKey.ID, bob.ID}} {
		keyID, userID := item.keyID, item.userID
		if err := repo.RecordRequest(time.UTC, RequestLog{
			SubscriptionID: &subscription.ID, APIKeyID: &keyID, UserID: &userID, Provider: "codex", Method: "POST", Path: "/v1/responses",
			StatusCode: 200, StartedAt: started.Add(time.Duration(i) * time.Minute), FinishedAt: started.Add(time.Duration(i)*time.Minute + time.Second),
			Model: "gpt-test",
		}); err != nil {
			t.Fatal(err)
		}
	}
	logs, total, err := repo.ListRequestLogs(ctx, time.UTC, RequestLogFilter{Query: "ali", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(logs) != 1 || logs[0].Username != "alice" || logs[0].UserID == nil || *logs[0].UserID != alice.ID {
		t.Fatalf("username search = total:%d data:%+v", total, logs)
	}
	detail, err := repo.GetRequestLog(ctx, logs[0].Day, logs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Username != "alice" || detail.UserID == nil || *detail.UserID != alice.ID {
		t.Fatalf("detail username = %+v", detail)
	}

	// API Key / subscription names are not part of free-text search.
	keyNamed, _, err := repo.CreateAPIKey(ctx, CreateAPIKeyParams{SubscriptionID: subscription.ID, UserID: &bob.ID, Name: "alice-shadow-key"})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordRequest(time.UTC, RequestLog{
		SubscriptionID: &subscription.ID, APIKeyID: &keyNamed.ID, UserID: &bob.ID, Provider: "codex", Method: "POST", Path: "/v1/responses",
		StatusCode: 200, StartedAt: started.Add(2 * time.Minute), FinishedAt: started.Add(2*time.Minute + time.Second),
		Model: "gpt-test",
	}); err != nil {
		t.Fatal(err)
	}
	logs, total, err = repo.ListRequestLogs(ctx, time.UTC, RequestLogFilter{Query: "shadow", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(logs) != 0 {
		t.Fatalf("api key name should not match free-text search: total:%d data:%+v", total, logs)
	}
}

func TestUsageByUserAttributesRequestsToAPIKeyIssuer(t *testing.T) {
	repo := testRepository(t)
	ctx := context.Background()
	alice, err := repo.CreateUser(ctx, "alice", "alice-password-ok", model.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := repo.CreateUser(ctx, "bob", "bob-password-ok", model.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	charlie, err := repo.CreateUser(ctx, "charlie", "charlie-password-ok", model.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	subscription, err := repo.CreateSubscription(ctx, CreateSubscriptionParams{
		Name: "shared", Provider: model.ProviderCodex, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	aliceKey, _, err := repo.CreateAPIKey(ctx, CreateAPIKeyParams{SubscriptionID: subscription.ID, UserID: &alice.ID, Name: "alice-key"})
	if err != nil {
		t.Fatal(err)
	}
	bobKey, _, err := repo.CreateAPIKey(ctx, CreateAPIKeyParams{SubscriptionID: subscription.ID, UserID: &bob.ID, Name: "bob-key"})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 9, 7, 8, 0, 0, 0, time.UTC)
	input10, output4 := int64(10), int64(4)
	input7, output3 := int64(7), int64(3)
	for _, entry := range []RequestLog{
		{SubscriptionID: &subscription.ID, APIKeyID: &aliceKey.ID, UserID: &alice.ID, Provider: "codex", Method: "POST", Path: "/v1/responses", StatusCode: 200, StartedAt: started, FinishedAt: started.Add(time.Second), InputTokens: &input10, OutputTokens: &output4},
		// A legacy row without user_id falls back to the API key issuer.
		{SubscriptionID: &subscription.ID, APIKeyID: &bobKey.ID, Provider: "codex", Method: "POST", Path: "/v1/responses", StatusCode: 200, StartedAt: started.Add(time.Minute), FinishedAt: started.Add(time.Minute + time.Second), InputTokens: &input7, OutputTokens: &output3},
		{SubscriptionID: &subscription.ID, Provider: "codex", Method: "POST", Path: "/v1/responses", StatusCode: 401, StartedAt: started.Add(2 * time.Minute), FinishedAt: started.Add(2*time.Minute + time.Second)},
	} {
		if err := repo.RecordRequest(time.UTC, entry); err != nil {
			t.Fatal(err)
		}
	}
	rows, totals, _, err := repo.UsageByUser(ctx, started.Add(-time.Hour), started.Add(time.Hour), time.UTC, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("user usage rows = %+v", rows)
	}
	for _, row := range rows {
		if row.UserID != nil && *row.UserID == charlie.ID {
			t.Fatalf("zero-usage user should be excluded: %+v", row)
		}
	}
	if rows[0].UserID == nil || *rows[0].UserID != alice.ID || rows[0].Usage.Requests != 1 || rows[0].Usage.TotalTokens != 14 {
		t.Fatalf("alice usage = %+v", rows[0])
	}
	if rows[1].UserID == nil || *rows[1].UserID != bob.ID || rows[1].Usage.Requests != 1 || rows[1].Usage.TotalTokens != 10 {
		t.Fatalf("bob usage = %+v", rows[1])
	}
	if rows[2].UserID != nil || rows[2].Username != "未归属" || rows[2].Usage.Requests != 1 {
		t.Fatalf("unassigned usage = %+v", rows[2])
	}
	if totals.Requests != 3 || totals.InputTokens != 17 || totals.OutputTokens != 7 || totals.TotalTokens != 24 {
		t.Fatalf("usage totals = %+v", totals)
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
