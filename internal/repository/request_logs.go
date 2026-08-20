package repository

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"time"
)

var requestLogTablePattern = regexp.MustCompile(`^request_logs_([0-9]{8})$`)

type RequestLog struct {
	AccountID  *int64
	APIKeyID   *int64
	Provider   string
	Method     string
	Path       string
	StatusCode int
	StartedAt  time.Time
	FinishedAt time.Time
	RequestID  string
	ErrorType  string
}

func (r *Repository) RecordRequest(location *time.Location, entry RequestLog) error {
	if location == nil {
		location = time.Local
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	table := requestLogTableName(entry.StartedAt, location)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := createRequestLogTable(ctx, tx, table); err != nil {
		return err
	}
	duration := entry.FinishedAt.Sub(entry.StartedAt)
	if duration < 0 {
		duration = 0
	}
	query := fmt.Sprintf(`INSERT INTO %s
		(account_id, api_key_id, provider, method, path, status_code, started_at, finished_at, duration_ms, request_id, error_type)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`, table)
	_, err = tx.ExecContext(ctx, query, nullableInt64(entry.AccountID), nullableInt64(entry.APIKeyID), nullableString(entry.Provider),
		entry.Method, entry.Path, entry.StatusCode, entry.StartedAt.UTC().Format(time.RFC3339Nano),
		entry.FinishedAt.UTC().Format(time.RFC3339Nano), duration.Milliseconds(), nullableString(entry.RequestID), nullableString(entry.ErrorType))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repository) CleanupRequestLogTables(ctx context.Context, now time.Time, location *time.Location, retentionDays int) ([]string, error) {
	if location == nil {
		location = time.Local
	}
	if retentionDays < 1 {
		retentionDays = 30
	}
	localNow := now.In(location)
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)
	oldestRetainedDate := today.AddDate(0, 0, -(retentionDays - 1))
	rows, err := r.db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'request_logs_%'`)
	if err != nil {
		return nil, err
	}
	var expired []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			rows.Close()
			return nil, err
		}
		match := requestLogTablePattern.FindStringSubmatch(table)
		if match == nil {
			continue
		}
		date, err := time.ParseInLocation("20060102", match[1], location)
		if err == nil && date.Before(oldestRetainedDate) {
			expired = append(expired, table)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	sort.Strings(expired)
	for _, table := range expired {
		if _, err := r.db.ExecContext(ctx, `DROP TABLE IF EXISTS `+table); err != nil {
			return nil, fmt.Errorf("drop expired request log table %s: %w", table, err)
		}
	}
	return expired, nil
}

func requestLogTableName(at time.Time, location *time.Location) string {
	return "request_logs_" + at.In(location).Format("20060102")
}

func createRequestLogTable(ctx context.Context, tx *sql.Tx, table string) error {
	if !requestLogTablePattern.MatchString(table) {
		return fmt.Errorf("invalid request log table name %q", table)
	}
	statement := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
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
		request_id TEXT,
		error_type TEXT
	)`, table)
	if _, err := tx.ExecContext(ctx, statement); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%s_account_started ON %s(account_id, started_at DESC)`, table, table))
	return err
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
