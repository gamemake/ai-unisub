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
	rows, err := db.Query(`PRAGMA table_info(accounts)`)
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
	for _, name := range []string{"credentials_encrypted", "credential_key_id", "proxy_url_encrypted"} {
		if columns[name] {
			t.Fatalf("legacy column %s was not removed", name)
		}
	}
	var credentials, proxyURL string
	if err := db.QueryRow(`SELECT credentials_json, proxy_url FROM accounts WHERE id=1`).Scan(&credentials, &proxyURL); err != nil {
		t.Fatal(err)
	}
	if credentials != `{"access_token":"legacy-value"}` || proxyURL != "http://127.0.0.1:3128" {
		t.Fatalf("migrated plaintext values = %q / %q", credentials, proxyURL)
	}
	var seconds int
	if err := db.QueryRow(`SELECT concurrency_queue_timeout_seconds FROM accounts WHERE id=1`).Scan(&seconds); err != nil {
		t.Fatal(err)
	}
	if seconds != 3 {
		t.Fatalf("converted queue timeout = %d seconds", seconds)
	}
	if _, err := db.Exec(`UPDATE accounts SET concurrency_queue_timeout_seconds=0 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT concurrency_queue_timeout_seconds FROM accounts WHERE id=1`).Scan(&seconds); err != nil {
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
	if _, err := db.Exec(`INSERT INTO api_keys(account_id, name, key_hash, key_prefix) VALUES(1, 'second', X'02', 'unisub_second')`); err != nil {
		t.Fatalf("second key was rejected: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM api_keys WHERE account_id=1`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("key count = %d err=%v", count, err)
	}
}
