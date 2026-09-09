package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func openPostgres(ctx context.Context, dsn string) (*DB, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres DSN: %w", err)
	}
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	if cfg.RuntimeParams["TimeZone"] == "" {
		cfg.RuntimeParams["TimeZone"] = "UTC"
	}
	raw := stdlib.OpenDB(*cfg)
	raw.SetMaxOpenConns(25)
	raw.SetMaxIdleConns(5)
	raw.SetConnMaxLifetime(30 * time.Minute)
	raw.SetConnMaxIdleTime(5 * time.Minute)
	if err := raw.PingContext(ctx); err != nil {
		raw.Close()
		return nil, err
	}
	if err := migratePostgres(ctx, raw); err != nil {
		raw.Close()
		return nil, err
	}
	return &DB{raw: raw, Dialect: PostgreSQL}, nil
}

func migratePostgres(ctx context.Context, db *sql.DB) error {
	for _, statement := range currentSchema(PostgreSQL) {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("database migration failed: %w", err)
		}
	}
	if err := migratePostgresAccountsToSubscriptions(ctx, db); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE subscriptions DROP COLUMN IF EXISTS auth_type`); err != nil {
		return fmt.Errorf("drop subscriptions.auth_type: %w", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_api_keys_subscription ON api_keys(subscription_id)`); err != nil {
		return fmt.Errorf("create api_keys subscription index: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, version := range []int{1, 2, 3, 4, 5, 6, 7, 9, 10, 11, 12} {
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES($1, $2) ON CONFLICT (version) DO NOTHING`, version, now); err != nil {
			return fmt.Errorf("record postgres schema version %d: %w", version, err)
		}
	}
	return nil
}

func migratePostgresAccountsToSubscriptions(ctx context.Context, db *sql.DB) error {
	hasAccounts, err := tableExists(ctx, dbQuerier{db}, PostgreSQL, "accounts")
	if err != nil {
		return err
	}
	hasSubscriptions, err := tableExists(ctx, dbQuerier{db}, PostgreSQL, "subscriptions")
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
		if _, err := db.ExecContext(ctx, `ALTER TABLE subscriptions DROP COLUMN IF EXISTS auth_type`); err != nil {
			return fmt.Errorf("drop legacy subscriptions.auth_type: %w", err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO subscriptions(
			id, name, provider, credentials_json, metadata_json, proxy_url, status, enabled,
			concurrency_limit, concurrency_queue_timeout_seconds, token_expires_at, rate_limit_reset_at,
			quota_json, quota_checked_at, quota_error, last_used_at, last_error, created_at, updated_at)
			SELECT id, name, provider, credentials_json, metadata_json, proxy_url, status, enabled,
			concurrency_limit, concurrency_queue_timeout_seconds, token_expires_at, rate_limit_reset_at,
			quota_json, quota_checked_at, quota_error, last_used_at, last_error, created_at, updated_at
			FROM accounts
			ON CONFLICT (id) DO NOTHING`); err != nil {
			return fmt.Errorf("copy accounts into subscriptions: %w", err)
		}
		if _, err := db.ExecContext(ctx, `DROP TABLE accounts`); err != nil {
			return fmt.Errorf("drop legacy accounts: %w", err)
		}
	}
	if _, err := db.ExecContext(ctx, `DROP INDEX IF EXISTS idx_accounts_created_by`); err != nil {
		return fmt.Errorf("drop accounts created_by index: %w", err)
	}
	cols, err := columnNames(ctx, dbQuerier{db}, PostgreSQL, "subscriptions")
	if err != nil {
		return err
	}
	if cols["created_by_user_id"] {
		if _, err := db.ExecContext(ctx, `ALTER TABLE subscriptions DROP COLUMN IF EXISTS created_by_user_id`); err != nil {
			return fmt.Errorf("drop subscriptions.created_by_user_id: %w", err)
		}
	}
	if err := migratePostgresAPIKeys(ctx, db); err != nil {
		return err
	}
	return migratePostgresRequestLogs(ctx, db)
}

func migratePostgresAPIKeys(ctx context.Context, db *sql.DB) error {
	cols, err := columnNames(ctx, dbQuerier{db}, PostgreSQL, "api_keys")
	if err != nil {
		return err
	}
	if cols["account_id"] && !cols["subscription_id"] {
		if _, err := db.ExecContext(ctx, `ALTER TABLE api_keys RENAME COLUMN account_id TO subscription_id`); err != nil {
			return fmt.Errorf("rename api_keys.account_id: %w", err)
		}
	}
	if !cols["user_id"] {
		if _, err := db.ExecContext(ctx, `ALTER TABLE api_keys ADD COLUMN user_id BIGINT`); err != nil {
			return fmt.Errorf("add api_keys.user_id: %w", err)
		}
	}
	if _, err := db.ExecContext(ctx, `DROP INDEX IF EXISTS idx_api_keys_account`); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_api_keys_subscription ON api_keys(subscription_id)`); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_api_keys_user ON api_keys(user_id)`); err != nil {
		return err
	}
	return nil
}

func migratePostgresRequestLogs(ctx context.Context, db *sql.DB) error {
	tables, err := listTablesLike(ctx, dbQuerier{db}, PostgreSQL, "request_logs_%")
	if err != nil {
		return err
	}
	for _, table := range tables {
		if !validIdent(table) {
			continue
		}
		cols, err := columnNames(ctx, dbQuerier{db}, PostgreSQL, table)
		if err != nil {
			return err
		}
		for _, column := range []string{"raw_request_headers", "raw_upstream_request_headers"} {
			if cols[column] {
				if _, err := db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s DROP COLUMN %s`, table, column)); err != nil {
					return fmt.Errorf("drop %s.%s: %w", table, column, err)
				}
			}
		}
		if cols["account_id"] && !cols["subscription_id"] {
			if _, err := db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s RENAME COLUMN account_id TO subscription_id`, table)); err != nil {
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
