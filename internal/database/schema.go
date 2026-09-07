package database

import (
	"context"
	"fmt"
)

func currentSchema(d Dialect) []string {
	pk := d.SerialPrimaryKey()
	blob := d.BlobType()
	big := d.BigIntType()
	return []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS subscriptions (
			id %s,
			name TEXT NOT NULL,
			provider TEXT NOT NULL CHECK (provider IN ('claude','codex','grok')),
			auth_type TEXT NOT NULL CHECK (auth_type IN ('oauth','api_key')),
			credentials_json %s NOT NULL,
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
		)`, pk, blob),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS api_keys (
			id %s,
			subscription_id %s NOT NULL,
			user_id %s,
			name TEXT NOT NULL,
			key_hash %s NOT NULL UNIQUE,
			key_plaintext TEXT,
			key_prefix TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
			rpm_limit INTEGER,
			concurrency INTEGER,
			expires_at TEXT,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (subscription_id) REFERENCES subscriptions(id) ON DELETE CASCADE
		)`, pk, big, big, blob),
		`CREATE TABLE IF NOT EXISTS admin (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			totp_secret TEXT,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS users (
			id %s,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'admin' CHECK (role IN ('admin','user')),
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`, pk),
	}
}

func RequestLogTableSQL(d Dialect, table string) (string, error) {
	if !validIdent(table) {
		return "", fmt.Errorf("invalid request log table name %q", table)
	}
	big := d.BigIntType()
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		id %s,
		subscription_id %s,
		api_key_id %s,
		user_id %s,
		provider TEXT,
		method TEXT NOT NULL,
		path TEXT NOT NULL,
		query TEXT,
		client_ip TEXT,
		status_code INTEGER NOT NULL,
		started_at TEXT NOT NULL,
		finished_at TEXT NOT NULL,
		duration_ms %s NOT NULL,
		request_id TEXT,
		error_type TEXT,
		model TEXT,
		input_tokens %s,
		output_tokens %s,
		cache_read_tokens %s,
		cache_creation_tokens %s,
		total_tokens %s,
		request_headers TEXT,
		request_body TEXT,
		response_headers TEXT,
		response_body TEXT,
		request_truncated INTEGER NOT NULL DEFAULT 0,
		response_truncated INTEGER NOT NULL DEFAULT 0
	)`, table, d.SerialPrimaryKey(), big, big, big, big, big, big, big, big, big), nil
}

func RequestLogColumnDefs(d Dialect) []struct{ Name, Definition string } {
	big := d.BigIntType()
	return []struct{ Name, Definition string }{
		{"user_id", big},
		{"query", "TEXT"},
		{"client_ip", "TEXT"},
		{"model", "TEXT"},
		{"input_tokens", big},
		{"output_tokens", big},
		{"cache_read_tokens", big},
		{"cache_creation_tokens", big},
		{"total_tokens", big},
		{"request_headers", "TEXT"},
		{"request_body", "TEXT"},
		{"response_headers", "TEXT"},
		{"response_body", "TEXT"},
		{"request_truncated", "INTEGER NOT NULL DEFAULT 0"},
		{"response_truncated", "INTEGER NOT NULL DEFAULT 0"},
	}
}

func tableExists(ctx context.Context, q querier, dialect Dialect, name string) (bool, error) {
	if !validIdent(name) {
		return false, fmt.Errorf("invalid table name %q", name)
	}
	var query string
	if dialect == PostgreSQL {
		query = `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name=?`
	} else {
		query = `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`
	}
	rows, err := q.QueryContext(ctx, query, name)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return false, err
		}
		return false, nil
	}
	var count int
	if err := rows.Scan(&count); err != nil {
		return false, err
	}
	return count > 0, rows.Err()
}

func listTablesLike(ctx context.Context, q querier, dialect Dialect, pattern string) ([]string, error) {
	var query string
	if dialect == PostgreSQL {
		query = `SELECT table_name FROM information_schema.tables WHERE table_schema=current_schema() AND table_name LIKE ?`
	} else {
		query = `SELECT name FROM sqlite_master WHERE type='table' AND name LIKE ?`
	}
	rows, err := q.QueryContext(ctx, query, pattern)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables = append(tables, name)
	}
	return tables, rows.Err()
}

func columnNames(ctx context.Context, q querier, dialect Dialect, table string) (map[string]bool, error) {
	if !validIdent(table) {
		return nil, fmt.Errorf("invalid table name %q", table)
	}
	existing := map[string]bool{}
	if dialect == PostgreSQL {
		rows, err := q.QueryContext(ctx, `SELECT column_name FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=?`, table)
		if err != nil {
			return nil, fmt.Errorf("inspect database schema: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return nil, fmt.Errorf("inspect database schema: %w", err)
			}
			existing[name] = true
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("inspect database schema: %w", err)
		}
		return existing, nil
	}
	rows, err := q.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return nil, fmt.Errorf("inspect database schema: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, fmt.Errorf("inspect database schema: %w", err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inspect database schema: %w", err)
	}
	return existing, nil
}
