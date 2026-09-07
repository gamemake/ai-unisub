package repository

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/database"
	"github.com/ai-unisub/ai-unisub/internal/model"
)

func TestPostgresSubscriptionAndRequestLogLifecycle(t *testing.T) {
	repo := testPostgresRepository(t)
	ctx := context.Background()
	created, err := repo.BootstrapAdmin(ctx, "admin", "correct horse battery staple")
	if err != nil || !created {
		t.Fatalf("bootstrap: created=%v err=%v", created, err)
	}
	account, err := repo.CreateSubscription(ctx, CreateSubscriptionParams{
		Name: "pg-codex", Provider: model.ProviderCodex, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "upstream-secret"},
		ProxyURL:    "socks5://127.0.0.1:1080",
	})
	if err != nil {
		t.Fatal(err)
	}
	key, plaintext, err := repo.CreateAPIKey(ctx, CreateAPIKeyParams{SubscriptionID: account.ID, Name: "default"})
	if err != nil || plaintext == "" || key.SubscriptionID != account.ID {
		t.Fatalf("api key = %+v plaintext=%q err=%v", key, plaintext, err)
	}
	resolved, err := repo.ResolveAPIKey(ctx, plaintext)
	if err != nil || resolved.ID != account.ID {
		t.Fatalf("resolve = %+v err=%v", resolved, err)
	}
	started := time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC)
	if err := repo.RecordRequest(time.UTC, RequestLog{
		SubscriptionID: &account.ID, APIKeyID: &key.ID, Provider: "codex",
		Method: "POST", Path: "/v1/responses", StatusCode: 200,
		StartedAt: started, FinishedAt: started.Add(12 * time.Millisecond),
		Model: "gpt-5",
	}); err != nil {
		t.Fatal(err)
	}
	logs, total, err := repo.ListRequestLogs(ctx, time.UTC, RequestLogFilter{SubscriptionID: &account.ID, Limit: 10})
	if err != nil || total != 1 || len(logs) != 1 || logs[0].Path != "/v1/responses" {
		t.Fatalf("logs=%+v total=%d err=%v", logs, total, err)
	}
	if _, err := repo.GetRequestLog(ctx, "20260819", logs[0].ID); err != nil {
		t.Fatal(err)
	}
}

func testPostgresRepository(t *testing.T) *Repository {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("UNISUB_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set UNISUB_TEST_POSTGRES_DSN to run postgres tests")
	}
	schema := fmt.Sprintf("unisub_test_%d", time.Now().UnixNano())
	bootstrap, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.Exec(`CREATE SCHEMA ` + schema); err != nil {
		_ = bootstrap.Close()
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = bootstrap.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		_ = bootstrap.Close()
	})
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := database.Open(context.Background(), string(database.PostgreSQL), dsn+sep+"search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	repo := New(db)
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}
