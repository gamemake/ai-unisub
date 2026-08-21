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
			credentials_json BLOB NOT NULL,
			metadata_json TEXT NOT NULL DEFAULT '{}',
			proxy_url TEXT,
			status TEXT NOT NULL DEFAULT 'active',
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
			concurrency_limit INTEGER NOT NULL DEFAULT 1,
			concurrency_queue_timeout_seconds INTEGER NOT NULL DEFAULT 0,
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
			account_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			key_hash BLOB NOT NULL UNIQUE,
			key_plaintext TEXT,
			key_prefix TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
			rpm_limit INTEGER,
			concurrency INTEGER,
			expires_at TEXT,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_api_keys_account ON api_keys(account_id)`,
		`CREATE TABLE IF NOT EXISTS admin (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			totp_secret TEXT,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'admin' CHECK (role IN ('admin','user')),
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
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
	if err := ensureColumn(ctx, db, "accounts", "concurrency_queue_timeout_seconds", `ALTER TABLE accounts ADD COLUMN concurrency_queue_timeout_seconds INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(1, ?)`, now); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(2, ?)`, now); err != nil {
		return err
	}
	var migratedToSeconds int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=3`).Scan(&migratedToSeconds); err != nil {
		return err
	}
	if migratedToSeconds == 0 {
		hasMilliseconds, err := columnExists(ctx, db, "accounts", "concurrency_queue_timeout_ms")
		if err != nil {
			return err
		}
		if hasMilliseconds {
			if _, err := db.ExecContext(ctx, `UPDATE accounts
				SET concurrency_queue_timeout_seconds=CASE
					WHEN concurrency_queue_timeout_ms <= 0 THEN 0
					ELSE (concurrency_queue_timeout_ms + 999) / 1000
				END
				WHERE concurrency_queue_timeout_seconds=0`); err != nil {
				return fmt.Errorf("migrate concurrency queue timeout to seconds: %w", err)
			}
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(3, ?)`, now); err != nil {
			return err
		}
	}
	var migratedToPlaintext int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=4`).Scan(&migratedToPlaintext); err != nil {
		return err
	}
	if migratedToPlaintext == 0 {
		if err := renameColumnIfNeeded(ctx, db, "accounts", "credentials_encrypted", "credentials_json"); err != nil {
			return err
		}
		if err := ensureColumn(ctx, db, "accounts", "credentials_json", `ALTER TABLE accounts ADD COLUMN credentials_json BLOB NOT NULL DEFAULT '{}'`); err != nil {
			return err
		}
		if err := renameColumnIfNeeded(ctx, db, "accounts", "proxy_url_encrypted", "proxy_url"); err != nil {
			return err
		}
		if err := ensureColumn(ctx, db, "accounts", "proxy_url", `ALTER TABLE accounts ADD COLUMN proxy_url TEXT`); err != nil {
			return err
		}
		if err := dropColumnIfExists(ctx, db, "accounts", "credential_key_id"); err != nil {
			return err
		}
		if err := renameColumnIfNeeded(ctx, db, "admin", "totp_secret_enc", "totp_secret"); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(4, ?)`, now); err != nil {
			return err
		}
	}
	if err := rebuildAPIKeysWithoutAccountUnique(ctx, db, now); err != nil {
		return err
	}
	if err := migrateUsersFromAdmin(ctx, db, now); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, "api_keys", "key_plaintext", `ALTER TABLE api_keys ADD COLUMN key_plaintext TEXT`); err != nil {
		return err
	}
	return nil
}

func migrateUsersFromAdmin(ctx context.Context, db *sql.DB, now string) error {
	var migrated int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=6`).Scan(&migrated); err != nil {
		return err
	}
	if migrated != 0 {
		return nil
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO users(username, password_hash, role, enabled, created_at, updated_at)
		SELECT username, password_hash, 'admin', 1, created_at, updated_at FROM admin
		WHERE NOT EXISTS (SELECT 1 FROM users)`); err != nil {
		return fmt.Errorf("migrate admin users: %w", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(6, ?)`, now); err != nil {
		return err
	}
	return nil
}

func rebuildAPIKeysWithoutAccountUnique(ctx context.Context, db *sql.DB, now string) error {
	var migrated int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=5`).Scan(&migrated); err != nil {
		return err
	}
	if migrated != 0 {
		return nil
	}
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer db.ExecContext(ctx, `PRAGMA foreign_keys=ON`)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE api_keys_v5 (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		account_id INTEGER NOT NULL,
		name TEXT NOT NULL,
		key_hash BLOB NOT NULL UNIQUE,
		key_prefix TEXT NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
		rpm_limit INTEGER,
		concurrency INTEGER,
		expires_at TEXT,
		created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
	)`); err != nil {
		return fmt.Errorf("rebuild api_keys: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO api_keys_v5(id, account_id, name, key_hash, key_prefix, enabled, rpm_limit, concurrency, expires_at, created_at)
		SELECT id, account_id, name, key_hash, key_prefix, enabled, rpm_limit, concurrency, expires_at, created_at FROM api_keys`); err != nil {
		return fmt.Errorf("copy api_keys: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE api_keys`); err != nil {
		return fmt.Errorf("drop api_keys: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE api_keys_v5 RENAME TO api_keys`); err != nil {
		return fmt.Errorf("rename api_keys: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_api_keys_account ON api_keys(account_id)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(5, ?)`, now); err != nil {
		return err
	}
	return tx.Commit()
}

func dropColumnIfExists(ctx context.Context, db *sql.DB, table, column string) error {
	found, err := columnExists(ctx, db, table, column)
	if err != nil || !found {
		return err
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE `+table+` DROP COLUMN `+column); err != nil {
		return fmt.Errorf("drop %s.%s: %w", table, column, err)
	}
	return nil
}

func renameColumnIfNeeded(ctx context.Context, db *sql.DB, table, oldColumn, newColumn string) error {
	hasNew, err := columnExists(ctx, db, table, newColumn)
	if err != nil || hasNew {
		return err
	}
	hasOld, err := columnExists(ctx, db, table, oldColumn)
	if err != nil || !hasOld {
		return err
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE `+table+` RENAME COLUMN `+oldColumn+` TO `+newColumn); err != nil {
		return fmt.Errorf("rename %s.%s to %s: %w", table, oldColumn, newColumn, err)
	}
	return nil
}

func ensureColumn(ctx context.Context, db *sql.DB, table, column, alterStatement string) error {
	found, err := columnExists(ctx, db, table, column)
	if err != nil {
		return err
	}
	if found {
		return nil
	}
	if _, err := db.ExecContext(ctx, alterStatement); err != nil {
		return fmt.Errorf("database migration failed: %w", err)
	}
	return nil
}

func columnExists(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, fmt.Errorf("inspect database schema: %w", err)
	}
	found := false
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return false, fmt.Errorf("inspect database schema: %w", err)
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, fmt.Errorf("inspect database schema: %w", err)
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	return found, nil
}
