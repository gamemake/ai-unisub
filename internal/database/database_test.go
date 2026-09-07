package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrateConvertsConcurrencyQueueTimeoutToSeconds(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE accounts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		credentials_encrypted BLOB NOT NULL,
		credential_key_id TEXT NOT NULL,
		proxy_url_encrypted BLOB,
		concurrency_queue_timeout_ms INTEGER NOT NULL DEFAULT 0
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO accounts(credentials_encrypted, credential_key_id, proxy_url_encrypted, concurrency_queue_timeout_ms)
		VALUES('{"access_token":"legacy-value"}', 'old-key', 'http://127.0.0.1:3128', 2500)`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	found := false
	columns := map[string]bool{}
	rows, err := db.Query(`PRAGMA table_info(subscriptions)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		if name == "concurrency_queue_timeout_seconds" {
			found = true
		}
		columns[name] = true
	}
	if !found {
		t.Fatal("concurrency_queue_timeout_seconds column was not added")
	}
	for _, name := range []string{"credentials_json", "proxy_url"} {
		if !columns[name] {
			t.Fatalf("%s column was not created", name)
		}
	}
	for _, name := range []string{"credentials_encrypted", "credential_key_id", "proxy_url_encrypted", "created_by_user_id"} {
		if columns[name] {
			t.Fatalf("legacy column %s was not removed", name)
		}
	}
	var credentials, proxyURL string
	if err := db.QueryRow(`SELECT credentials_json, proxy_url FROM subscriptions WHERE id=1`).Scan(&credentials, &proxyURL); err != nil {
		t.Fatal(err)
	}
	if credentials != `{"access_token":"legacy-value"}` || proxyURL != "http://127.0.0.1:3128" {
		t.Fatalf("migrated plaintext values = %q / %q", credentials, proxyURL)
	}
	var seconds int
	if err := db.QueryRow(`SELECT concurrency_queue_timeout_seconds FROM subscriptions WHERE id=1`).Scan(&seconds); err != nil {
		t.Fatal(err)
	}
	if seconds != 3 {
		t.Fatalf("converted queue timeout = %d seconds", seconds)
	}
	if _, err := db.Exec(`UPDATE subscriptions SET concurrency_queue_timeout_seconds=0 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT concurrency_queue_timeout_seconds FROM subscriptions WHERE id=1`).Scan(&seconds); err != nil {
		t.Fatal(err)
	}
	if seconds != 0 {
		t.Fatalf("completed migration overwrote inherited value: %d", seconds)
	}
}

func TestMigrateRemovesAPIKeyAccountUniqueConstraint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE accounts (id INTEGER PRIMARY KEY, name TEXT);
		CREATE TABLE api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			account_id INTEGER NOT NULL UNIQUE,
			name TEXT NOT NULL,
			key_hash BLOB NOT NULL UNIQUE,
			key_prefix TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			rpm_limit INTEGER,
			concurrency INTEGER,
			expires_at TEXT,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO accounts(id, name) VALUES(1, 'shared');
		INSERT INTO api_keys(account_id, name, key_hash, key_prefix) VALUES(1, 'first', X'01', 'unisub_first');`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO api_keys(subscription_id, name, key_hash, key_prefix) VALUES(1, 'second', X'02', 'unisub_second')`); err != nil {
		t.Fatalf("second key was rejected: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM api_keys WHERE subscription_id=1`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("key count = %d err=%v", count, err)
	}
}

func TestMigrateAccountsToSubscriptions(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "created-by.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE accounts (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			provider TEXT NOT NULL DEFAULT 'grok',
			auth_type TEXT NOT NULL DEFAULT 'oauth',
			credentials_json BLOB NOT NULL DEFAULT '{}',
			metadata_json TEXT NOT NULL DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'active',
			enabled INTEGER NOT NULL DEFAULT 1,
			concurrency_limit INTEGER NOT NULL DEFAULT 1,
			concurrency_queue_timeout_seconds INTEGER NOT NULL DEFAULT 0,
			created_by_user_id INTEGER,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			account_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			key_hash BLOB NOT NULL UNIQUE,
			key_prefix TEXT NOT NULL,
			key_plaintext TEXT,
			enabled INTEGER NOT NULL DEFAULT 1,
			rpm_limit INTEGER,
			concurrency INTEGER,
			expires_at TEXT,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE request_logs_20260101 (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			account_id INTEGER,
			api_key_id INTEGER,
			provider TEXT,
			method TEXT NOT NULL,
			path TEXT NOT NULL,
			status_code INTEGER NOT NULL,
			started_at TEXT NOT NULL,
			finished_at TEXT NOT NULL,
			duration_ms INTEGER NOT NULL,
			request_truncated INTEGER NOT NULL DEFAULT 0,
			response_truncated INTEGER NOT NULL DEFAULT 0
		);
		INSERT INTO accounts(id, name, created_by_user_id) VALUES(1, 'legacy', 7);
		INSERT INTO api_keys(account_id, name, key_hash, key_prefix, key_plaintext) VALUES(1, 'default', X'01', 'unisub_legacy', 'unisub_secret');
		INSERT INTO request_logs_20260101(account_id, method, path, status_code, started_at, finished_at, duration_ms)
			VALUES(1, 'POST', '/v1/responses', 200, '2026-01-01T00:00:00Z', '2026-01-01T00:00:01Z', 1000);
		CREATE INDEX idx_request_logs_20260101_account_started ON request_logs_20260101(account_id, started_at DESC);`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	var accounts, subscriptions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='accounts'`).Scan(&accounts); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='subscriptions'`).Scan(&subscriptions); err != nil {
		t.Fatal(err)
	}
	if accounts != 0 || subscriptions != 1 {
		t.Fatalf("accounts=%d subscriptions=%d", accounts, subscriptions)
	}

	columns := map[string]bool{}
	rows, err := db.Query(`PRAGMA table_info(subscriptions)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		columns[name] = true
	}
	if columns["created_by_user_id"] {
		t.Fatal("created_by_user_id was not dropped")
	}

	var subscriptionID int64
	var plaintext string
	if err := db.QueryRow(`SELECT subscription_id, key_plaintext FROM api_keys WHERE id=1`).Scan(&subscriptionID, &plaintext); err != nil {
		t.Fatal(err)
	}
	if subscriptionID != 1 || plaintext != "unisub_secret" {
		t.Fatalf("api_keys = %d / %q", subscriptionID, plaintext)
	}

	logCols, err := columnNames(context.Background(), dbQuerier{db}, SQLite, "request_logs_20260101")
	if err != nil {
		t.Fatal(err)
	}
	if !logCols["subscription_id"] || logCols["account_id"] {
		t.Fatalf("request log columns = %#v", logCols)
	}
	var logSubscriptionID sql.NullInt64
	if err := db.QueryRow(`SELECT subscription_id FROM request_logs_20260101 WHERE id=1`).Scan(&logSubscriptionID); err != nil {
		t.Fatal(err)
	}
	if !logSubscriptionID.Valid || logSubscriptionID.Int64 != 1 {
		t.Fatalf("request log subscription_id = %#v", logSubscriptionID)
	}

	var migrated int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=10`).Scan(&migrated); err != nil {
		t.Fatal(err)
	}
	if migrated != 1 {
		t.Fatalf("migration version 10 count = %d", migrated)
	}
	apiKeyColumns, err := columnNames(context.Background(), dbQuerier{db}, SQLite, "api_keys")
	if err != nil {
		t.Fatal(err)
	}
	if !apiKeyColumns["user_id"] {
		t.Fatalf("api key columns = %#v", apiKeyColumns)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=11`).Scan(&migrated); err != nil {
		t.Fatal(err)
	}
	if migrated != 1 {
		t.Fatalf("migration version 11 count = %d", migrated)
	}
}

func TestMigrateDropsLegacyUsageLogs(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE usage_logs (id INTEGER PRIMARY KEY, account_id INTEGER NOT NULL);
		INSERT INTO usage_logs(id, account_id) VALUES(1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var tables int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='usage_logs'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatal("legacy usage_logs table was not dropped")
	}
	var migrated int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=7`).Scan(&migrated); err != nil {
		t.Fatal(err)
	}
	if migrated != 1 {
		t.Fatalf("migration version 7 count = %d", migrated)
	}
}
