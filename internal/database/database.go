package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

func Open(ctx context.Context, path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := Migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func Migrate(ctx context.Context, db *sql.DB) error {
	statements := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA foreign_keys=ON`,
		`PRAGMA busy_timeout=5000`,
		`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS accounts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			provider TEXT NOT NULL CHECK (provider IN ('claude','codex','grok')),
			auth_type TEXT NOT NULL CHECK (auth_type IN ('oauth','api_key')),
			credentials_encrypted BLOB NOT NULL,
			credential_key_id TEXT NOT NULL,
			metadata_json TEXT NOT NULL DEFAULT '{}',
			proxy_url_encrypted BLOB,
			status TEXT NOT NULL DEFAULT 'active',
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
			concurrency_limit INTEGER NOT NULL DEFAULT 1,
			token_expires_at TEXT,
			rate_limit_reset_at TEXT,
			quota_json TEXT,
			quota_checked_at TEXT,
			quota_error TEXT,
			last_used_at TEXT,
			last_error TEXT,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			account_id INTEGER NOT NULL UNIQUE,
			name TEXT NOT NULL,
			key_hash BLOB NOT NULL UNIQUE,
			key_prefix TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
			rpm_limit INTEGER,
			concurrency INTEGER,
			expires_at TEXT,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS admin (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			totp_secret_enc BLOB,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS usage_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			account_id INTEGER NOT NULL,
			provider TEXT NOT NULL,
			endpoint TEXT NOT NULL,
			model TEXT,
			status_code INTEGER NOT NULL,
			input_tokens INTEGER,
			output_tokens INTEGER,
			request_count INTEGER NOT NULL DEFAULT 1,
			started_at TEXT NOT NULL,
			finished_at TEXT,
			request_id TEXT,
			FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_logs_account_started ON usage_logs(account_id, started_at DESC)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("database migration failed: %w", err)
		}
	}
	_, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(1, ?)`, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
