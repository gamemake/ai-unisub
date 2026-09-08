package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func Open(ctx context.Context, driver, dsn string) (*DB, error) {
	dialect, err := ParseDriver(driver)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("database DSN is required")
	}
	switch dialect {
	case PostgreSQL:
		return openPostgres(ctx, dsn)
	case SQLite:
		return openSQLite(ctx, dsn)
	default:
		return nil, fmt.Errorf("unsupported database driver %q", driver)
	}
}

func OpenSQLite(ctx context.Context, path string) (*DB, error) {
	return Open(ctx, string(SQLite), path)
}

func openSQLite(ctx context.Context, path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	raw.SetMaxOpenConns(1)
	raw.SetConnMaxLifetime(0)
	if err := raw.PingContext(ctx); err != nil {
		raw.Close()
		return nil, err
	}
	if err := Migrate(ctx, raw); err != nil {
		raw.Close()
		return nil, err
	}
	return &DB{raw: raw, Dialect: SQLite}, nil
}

func Migrate(ctx context.Context, db *sql.DB) error {
	statements := append([]string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA foreign_keys=ON`,
		`PRAGMA busy_timeout=5000`,
	}, currentSchema(SQLite)...)
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("database migration failed: %w", err)
		}
	}
	if err := renameAccountsToSubscriptionsEarly(ctx, db); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, "subscriptions", "concurrency_queue_timeout_seconds", `ALTER TABLE subscriptions ADD COLUMN concurrency_queue_timeout_seconds INTEGER NOT NULL DEFAULT 0`); err != nil {
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
		hasMilliseconds, err := columnExists(ctx, db, "subscriptions", "concurrency_queue_timeout_ms")
		if err != nil {
			return err
		}
		if hasMilliseconds {
			if _, err := db.ExecContext(ctx, `UPDATE subscriptions
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
		if err := renameColumnIfNeeded(ctx, db, "subscriptions", "credentials_encrypted", "credentials_json"); err != nil {
			return err
		}
		if err := ensureColumn(ctx, db, "subscriptions", "credentials_json", `ALTER TABLE subscriptions ADD COLUMN credentials_json BLOB NOT NULL DEFAULT '{}'`); err != nil {
			return err
		}
		if err := renameColumnIfNeeded(ctx, db, "subscriptions", "proxy_url_encrypted", "proxy_url"); err != nil {
			return err
		}
		if err := ensureColumn(ctx, db, "subscriptions", "proxy_url", `ALTER TABLE subscriptions ADD COLUMN proxy_url TEXT`); err != nil {
			return err
		}
		if err := dropColumnIfExists(ctx, db, "subscriptions", "credential_key_id"); err != nil {
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
	if err := dropUsageLogs(ctx, db, now); err != nil {
		return err
	}
	if err := migrateAccountCreatedBy(ctx, db, now); err != nil {
		return err
	}
	if err := migrateAccountsToSubscriptions(ctx, db, now); err != nil {
		return err
	}
	if err := ensureAPIKeysSubscriptionIndex(ctx, db); err != nil {
		return err
	}
	if err := migrateAPIKeyUsers(ctx, db, now); err != nil {
		return err
	}
	if err := dropRequestLogRawHeaders(ctx, db); err != nil {
		return err
	}
	return nil
}

func dropRequestLogRawHeaders(ctx context.Context, db *sql.DB) error {
	tables, err := listTablesLike(ctx, dbQuerier{db}, SQLite, "request_logs_%")
	if err != nil {
		return err
	}
	for _, table := range tables {
		if !validIdent(table) {
			continue
		}
		for _, column := range []string{"raw_request_headers", "raw_upstream_request_headers"} {
			if err := dropColumnIfExists(ctx, db, table, column); err != nil {
				return err
			}
		}
	}
	return nil
}

func migrateAPIKeyUsers(ctx context.Context, db *sql.DB, now string) error {
	var migrated int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=11`).Scan(&migrated); err != nil {
		return err
	}
	if migrated != 0 {
		return nil
	}
	if err := ensureColumn(ctx, db, "api_keys", "user_id", `ALTER TABLE api_keys ADD COLUMN user_id INTEGER`); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_api_keys_user ON api_keys(user_id)`); err != nil {
		return fmt.Errorf("create api_keys user index: %w", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(11, ?)`, now); err != nil {
		return err
	}
	return nil
}

func ensureAPIKeysSubscriptionIndex(ctx context.Context, db *sql.DB) error {
	hasSubscriptionID, err := columnExists(ctx, db, "api_keys", "subscription_id")
	if err != nil || !hasSubscriptionID {
		return err
	}
	if _, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_api_keys_subscription ON api_keys(subscription_id)`); err != nil {
		return fmt.Errorf("create api_keys subscription index: %w", err)
	}
	return nil
}

func renameAccountsToSubscriptionsEarly(ctx context.Context, db *sql.DB) error {
	hasAccounts, err := sqliteTableExists(ctx, db, "accounts")
	if err != nil {
		return err
	}
	if !hasAccounts {
		return nil
	}
	hasSubscriptions, err := sqliteTableExists(ctx, db, "subscriptions")
	if err != nil {
		return err
	}
	if hasSubscriptions {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM subscriptions`).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if _, err := db.ExecContext(ctx, `DROP TABLE subscriptions`); err != nil {
				return fmt.Errorf("drop empty subscriptions: %w", err)
			}
		} else {
			return nil
		}
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE accounts RENAME TO subscriptions`); err != nil {
		return fmt.Errorf("rename accounts to subscriptions: %w", err)
	}
	return nil
}

func migrateAccountCreatedBy(ctx context.Context, db *sql.DB, now string) error {
	var migrated int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=9`).Scan(&migrated); err != nil {
		return err
	}
	if migrated != 0 {
		return nil
	}
	// Historical version 9 added created_by_user_id. Keep the version marker; v10 drops the column if present.
	if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(9, ?)`, now); err != nil {
		return err
	}
	return nil
}

func migrateAccountsToSubscriptions(ctx context.Context, db *sql.DB, now string) error {
	var migrated int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=10`).Scan(&migrated); err != nil {
		return err
	}
	if migrated != 0 {
		return nil
	}
	hasAccounts, err := sqliteTableExists(ctx, db, "accounts")
	if err != nil {
		return err
	}
	hasSubscriptions, err := sqliteTableExists(ctx, db, "subscriptions")
	if err != nil {
		return err
	}
	if hasAccounts && !hasSubscriptions {
		if _, err := db.ExecContext(ctx, `ALTER TABLE accounts RENAME TO subscriptions`); err != nil {
			return fmt.Errorf("rename accounts to subscriptions: %w", err)
		}
		hasAccounts = false
		hasSubscriptions = true
	}
	if hasAccounts && hasSubscriptions {
		if _, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO subscriptions(
			id, name, provider, auth_type, credentials_json, metadata_json, proxy_url, status, enabled,
			concurrency_limit, concurrency_queue_timeout_seconds, token_expires_at, rate_limit_reset_at,
			quota_json, quota_checked_at, quota_error, last_used_at, last_error, created_at, updated_at)
			SELECT id, name, provider, auth_type, credentials_json, metadata_json, proxy_url, status, enabled,
			concurrency_limit, concurrency_queue_timeout_seconds, token_expires_at, rate_limit_reset_at,
			quota_json, quota_checked_at, quota_error, last_used_at, last_error, created_at, updated_at
			FROM accounts`); err != nil {
			return fmt.Errorf("copy accounts into subscriptions: %w", err)
		}
		if _, err := db.ExecContext(ctx, `DROP TABLE accounts`); err != nil {
			return fmt.Errorf("drop legacy accounts: %w", err)
		}
	}
	if _, err := db.ExecContext(ctx, `DROP INDEX IF EXISTS idx_accounts_created_by`); err != nil {
		return fmt.Errorf("drop accounts created_by index: %w", err)
	}
	if err := dropColumnIfExists(ctx, db, "subscriptions", "created_by_user_id"); err != nil {
		return err
	}
	if err := rebuildAPIKeysSubscriptionID(ctx, db); err != nil {
		return err
	}
	if err := migrateRequestLogAccountColumns(ctx, db); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(10, ?)`, now); err != nil {
		return err
	}
	return nil
}

func rebuildAPIKeysSubscriptionID(ctx context.Context, db *sql.DB) error {
	hasAccountID, err := columnExists(ctx, db, "api_keys", "account_id")
	if err != nil {
		return err
	}
	hasSubscriptionID, err := columnExists(ctx, db, "api_keys", "subscription_id")
	if err != nil {
		return err
	}
	if !hasAccountID && hasSubscriptionID {
		if _, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_api_keys_subscription ON api_keys(subscription_id)`); err != nil {
			return err
		}
		return nil
	}
	if !hasAccountID && !hasSubscriptionID {
		return fmt.Errorf("api_keys missing subscription identity column")
	}
	hasPlaintext, err := columnExists(ctx, db, "api_keys", "key_plaintext")
	if err != nil {
		return err
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
	if _, err := tx.ExecContext(ctx, `CREATE TABLE api_keys_v10 (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		subscription_id INTEGER NOT NULL,
		name TEXT NOT NULL,
		key_hash BLOB NOT NULL UNIQUE,
		key_plaintext TEXT,
		key_prefix TEXT NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
		rpm_limit INTEGER,
		concurrency INTEGER,
		expires_at TEXT,
		created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (subscription_id) REFERENCES subscriptions(id) ON DELETE CASCADE
	)`); err != nil {
		return fmt.Errorf("rebuild api_keys for subscription_id: %w", err)
	}
	sourceColumn := "subscription_id"
	if hasAccountID {
		sourceColumn = "account_id"
	}
	plaintextSelect := "NULL"
	if hasPlaintext {
		plaintextSelect = "key_plaintext"
	}
	copySQL := fmt.Sprintf(`INSERT INTO api_keys_v10(id, subscription_id, name, key_hash, key_plaintext, key_prefix, enabled, rpm_limit, concurrency, expires_at, created_at)
		SELECT id, %s, name, key_hash, %s, key_prefix, enabled, rpm_limit, concurrency, expires_at, created_at FROM api_keys`, sourceColumn, plaintextSelect)
	if _, err := tx.ExecContext(ctx, copySQL); err != nil {
		return fmt.Errorf("copy api_keys to subscription_id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE api_keys`); err != nil {
		return fmt.Errorf("drop api_keys: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE api_keys_v10 RENAME TO api_keys`); err != nil {
		return fmt.Errorf("rename api_keys: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_api_keys_subscription ON api_keys(subscription_id)`); err != nil {
		return err
	}
	return tx.Commit()
}

func migrateRequestLogAccountColumns(ctx context.Context, db *sql.DB) error {
	tables, err := listTablesLike(ctx, dbQuerier{db}, SQLite, "request_logs_%")
	if err != nil {
		return err
	}
	for _, table := range tables {
		if !validIdent(table) {
			continue
		}
		cols, err := columnNames(ctx, dbQuerier{db}, SQLite, table)
		if err != nil {
			return err
		}
		if cols["account_id"] && !cols["subscription_id"] {
			if _, err := db.ExecContext(ctx, `ALTER TABLE `+table+` RENAME COLUMN account_id TO subscription_id`); err != nil {
				return fmt.Errorf("rename %s.account_id: %w", table, err)
			}
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf(`DROP INDEX IF EXISTS idx_%s_account_started`, table)); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%s_subscription_started ON %s(subscription_id, started_at DESC)`, table, table)); err != nil {
			return err
		}
	}
	return nil
}

type dbQuerier struct{ db *sql.DB }

func (q dbQuerier) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return q.db.QueryContext(ctx, query, args...)
}

func sqliteTableExists(ctx context.Context, db *sql.DB, name string) (bool, error) {
	return tableExists(ctx, dbQuerier{db}, SQLite, name)
}

func dropUsageLogs(ctx context.Context, db *sql.DB, now string) error {
	var migrated int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=7`).Scan(&migrated); err != nil {
		return err
	}
	if migrated != 0 {
		return nil
	}
	if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS usage_logs`); err != nil {
		return fmt.Errorf("drop legacy usage_logs: %w", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(7, ?)`, now); err != nil {
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
	hasAPIKeys, err := sqliteTableExists(ctx, db, "api_keys")
	if err != nil {
		return err
	}
	if !hasAPIKeys {
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(5, ?)`, now); err != nil {
			return err
		}
		return nil
	}
	hasAccountID, err := columnExists(ctx, db, "api_keys", "account_id")
	if err != nil {
		return err
	}
	hasSubscriptionID, err := columnExists(ctx, db, "api_keys", "subscription_id")
	if err != nil {
		return err
	}
	// Fresh schema already uses subscription_id without UNIQUE; just record v5.
	if hasSubscriptionID && !hasAccountID {
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(5, ?)`, now); err != nil {
			return err
		}
		return nil
	}
	if !hasAccountID && !hasSubscriptionID {
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(5, ?)`, now); err != nil {
			return err
		}
		return nil
	}
	sourceColumn := "account_id"
	if hasSubscriptionID && !hasAccountID {
		sourceColumn = "subscription_id"
	}
	hasSubscriptions, err := sqliteTableExists(ctx, db, "subscriptions")
	if err != nil {
		return err
	}
	parentTable := "accounts"
	if hasSubscriptions {
		parentTable = "subscriptions"
	}
	hasPlaintext, err := columnExists(ctx, db, "api_keys", "key_plaintext")
	if err != nil {
		return err
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
	plaintextColumn := ""
	if hasPlaintext {
		plaintextColumn = "\n\t\tkey_plaintext TEXT,"
	}
	createSQL := fmt.Sprintf(`CREATE TABLE api_keys_v5 (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		subscription_id INTEGER NOT NULL,
		name TEXT NOT NULL,
		key_hash BLOB NOT NULL UNIQUE,%s
		key_prefix TEXT NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
		rpm_limit INTEGER,
		concurrency INTEGER,
		expires_at TEXT,
		created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (subscription_id) REFERENCES %s(id) ON DELETE CASCADE
	)`, plaintextColumn, parentTable)
	if _, err := tx.ExecContext(ctx, createSQL); err != nil {
		return fmt.Errorf("rebuild api_keys: %w", err)
	}
	var copySQL string
	if hasPlaintext {
		copySQL = fmt.Sprintf(`INSERT INTO api_keys_v5(id, subscription_id, name, key_hash, key_plaintext, key_prefix, enabled, rpm_limit, concurrency, expires_at, created_at)
			SELECT id, %s, name, key_hash, key_plaintext, key_prefix, enabled, rpm_limit, concurrency, expires_at, created_at FROM api_keys`, sourceColumn)
	} else {
		copySQL = fmt.Sprintf(`INSERT INTO api_keys_v5(id, subscription_id, name, key_hash, key_prefix, enabled, rpm_limit, concurrency, expires_at, created_at)
			SELECT id, %s, name, key_hash, key_prefix, enabled, rpm_limit, concurrency, expires_at, created_at FROM api_keys`, sourceColumn)
	}
	if _, err := tx.ExecContext(ctx, copySQL); err != nil {
		return fmt.Errorf("copy api_keys: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE api_keys`); err != nil {
		return fmt.Errorf("drop api_keys: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE api_keys_v5 RENAME TO api_keys`); err != nil {
		return fmt.Errorf("rename api_keys: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_api_keys_subscription ON api_keys(subscription_id)`); err != nil {
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
	exists, err := sqliteTableExists(ctx, db, table)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
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
	exists, err := sqliteTableExists(ctx, db, table)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
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
