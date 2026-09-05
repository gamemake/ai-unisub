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
	if _, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_accounts_created_by ON accounts(created_by_user_id)`); err != nil {
		return fmt.Errorf("create accounts created_by index: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, version := range []int{1, 2, 3, 4, 5, 6, 7, 9} {
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES($1, $2) ON CONFLICT (version) DO NOTHING`, version, now); err != nil {
			return fmt.Errorf("record postgres schema version %d: %w", version, err)
		}
	}
	return nil
}
