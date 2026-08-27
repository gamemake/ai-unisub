package repository

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
)

var requestLogTablePattern = regexp.MustCompile(`^request_logs_([0-9]{8})$`)

type RequestLog struct {
	ID                  int64     `json:"id"`
	Day                 string    `json:"day"`
	AccountID           *int64    `json:"account_id,omitempty"`
	AccountName         string    `json:"account_name,omitempty"`
	APIKeyID            *int64    `json:"api_key_id,omitempty"`
	APIKeyName          string    `json:"api_key_name,omitempty"`
	APIKeyPrefix        string    `json:"api_key_prefix,omitempty"`
	Provider            string    `json:"provider,omitempty"`
	Method              string    `json:"method"`
	Path                string    `json:"path"`
	Query               string    `json:"query,omitempty"`
	ClientIP            string    `json:"client_ip,omitempty"`
	StatusCode          int       `json:"status_code"`
	StartedAt           time.Time `json:"started_at"`
	FinishedAt          time.Time `json:"finished_at"`
	DurationMs          int64     `json:"duration_ms"`
	RequestID           string    `json:"request_id,omitempty"`
	ErrorType           string    `json:"error_type,omitempty"`
	Model               string    `json:"model,omitempty"`
	InputTokens         *int64    `json:"input_tokens,omitempty"`
	OutputTokens        *int64    `json:"output_tokens,omitempty"`
	CacheReadTokens     *int64    `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens *int64    `json:"cache_creation_tokens,omitempty"`
	TotalTokens         *int64    `json:"total_tokens,omitempty"`
	RequestHeaders      string    `json:"request_headers,omitempty"`
	RequestBody         string    `json:"request_body,omitempty"`
	ResponseHeaders     string    `json:"response_headers,omitempty"`
	ResponseBody        string    `json:"response_body,omitempty"`
	RequestTruncated    bool      `json:"request_truncated"`
	ResponseTruncated   bool      `json:"response_truncated"`
}

type RequestLogFilter struct {
	AccountID  *int64
	AccountIDs []int64
	APIKeyID   *int64
	Provider   string
	StatusCode *int
	Query      string
	Limit      int
	Offset     int
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
		(account_id, api_key_id, provider, method, path, query, client_ip, status_code, started_at, finished_at, duration_ms,
		 request_id, error_type, model, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, total_tokens,
		 request_headers, request_body, response_headers, response_body, request_truncated, response_truncated)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, table)
	_, err = tx.ExecContext(ctx, query,
		nullableInt64(entry.AccountID), nullableInt64(entry.APIKeyID), nullableString(entry.Provider),
		entry.Method, entry.Path, nullableString(entry.Query), nullableString(entry.ClientIP), entry.StatusCode,
		entry.StartedAt.UTC().Format(time.RFC3339Nano), entry.FinishedAt.UTC().Format(time.RFC3339Nano), duration.Milliseconds(),
		nullableString(entry.RequestID), nullableString(entry.ErrorType), nullableString(entry.Model),
		nullableInt64(entry.InputTokens), nullableInt64(entry.OutputTokens), nullableInt64(entry.CacheReadTokens),
		nullableInt64(entry.CacheCreationTokens), nullableInt64(entry.TotalTokens),
		nullableString(entry.RequestHeaders), nullableString(entry.RequestBody),
		nullableString(entry.ResponseHeaders), nullableString(entry.ResponseBody),
		boolToInt(entry.RequestTruncated), boolToInt(entry.ResponseTruncated))
	if err != nil {
		return err
	}
	if entry.AccountID != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET last_used_at=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
			entry.FinishedAt.UTC().Format(time.RFC3339Nano), *entry.AccountID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *Repository) ListRequestLogs(ctx context.Context, location *time.Location, filter RequestLogFilter) ([]RequestLog, int, error) {
	if location == nil {
		location = time.Local
	}
	if filter.Limit <= 0 || filter.Limit > 200 {
		filter.Limit = 50
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	tables, err := r.requestLogTables(ctx)
	if err != nil {
		return nil, 0, err
	}
	if len(tables) == 0 {
		return []RequestLog{}, 0, nil
	}
	for _, table := range tables {
		if err := ensureRequestLogColumns(ctx, r.db, table); err != nil {
			return nil, 0, err
		}
	}
	unions := make([]string, 0, len(tables))
	for _, table := range tables {
		day := strings.TrimPrefix(table, "request_logs_")
		unions = append(unions, fmt.Sprintf(`SELECT '%s' AS day, l.id, l.account_id, a.name AS account_name, l.api_key_id, k.name AS api_key_name, k.key_prefix,
			l.provider, l.method, l.path, l.query, l.client_ip, l.status_code, l.started_at, l.finished_at, l.duration_ms, l.request_id, l.error_type,
			l.model, l.input_tokens, l.output_tokens, l.cache_read_tokens, l.cache_creation_tokens, l.total_tokens,
			l.request_truncated, l.response_truncated
			FROM %s l
			LEFT JOIN accounts a ON a.id=l.account_id
			LEFT JOIN api_keys k ON k.id=l.api_key_id`, day, table))
	}
	from := "(" + strings.Join(unions, " UNION ALL ") + ")"
	where, args := requestLogWhere(filter)
	var total int
	countQuery := "SELECT COUNT(*) FROM " + from + " logs " + where
	if err := r.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	listQuery := "SELECT day, id, account_id, account_name, api_key_id, api_key_name, key_prefix, provider, method, path, query, client_ip, status_code, started_at, finished_at, duration_ms, request_id, error_type, model, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, total_tokens, request_truncated, response_truncated FROM " +
		from + " logs " + where + " ORDER BY started_at DESC LIMIT ? OFFSET ?"
	rows, err := r.db.QueryContext(ctx, listQuery, append(append([]any{}, args...), filter.Limit, filter.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var logs []RequestLog
	for rows.Next() {
		entry, err := scanRequestLogSummary(rows)
		if err != nil {
			return nil, 0, err
		}
		logs = append(logs, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if logs == nil {
		logs = []RequestLog{}
	}
	return logs, total, nil
}

func (r *Repository) GetRequestLog(ctx context.Context, day string, id int64) (RequestLog, error) {
	if !requestLogTablePattern.MatchString("request_logs_" + day) {
		return RequestLog{}, ErrNotFound
	}
	table := "request_logs_" + day
	var exists int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&exists); err != nil {
		return RequestLog{}, err
	}
	if exists == 0 {
		return RequestLog{}, ErrNotFound
	}
	if err := ensureRequestLogColumns(ctx, r.db, table); err != nil {
		return RequestLog{}, err
	}
	query := fmt.Sprintf(`SELECT '%s' AS day, l.id, l.account_id, a.name AS account_name, l.api_key_id, k.name AS api_key_name, k.key_prefix,
		l.provider, l.method, l.path, l.query, l.client_ip, l.status_code, l.started_at, l.finished_at, l.duration_ms, l.request_id, l.error_type,
		l.model, l.input_tokens, l.output_tokens, l.cache_read_tokens, l.cache_creation_tokens, l.total_tokens,
		l.request_headers, l.request_body, l.response_headers, l.response_body, l.request_truncated, l.response_truncated
		FROM %s l
		LEFT JOIN accounts a ON a.id=l.account_id
		LEFT JOIN api_keys k ON k.id=l.api_key_id
		WHERE l.id=?`, day, table)
	row := r.db.QueryRowContext(ctx, query, id)
	entry, err := scanRequestLogDetail(row)
	if err == sql.ErrNoRows {
		return RequestLog{}, ErrNotFound
	}
	return entry, err
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

func (r *Repository) requestLogTables(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'request_logs_%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, err
		}
		if requestLogTablePattern.MatchString(table) {
			tables = append(tables, table)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(tables, func(i, j int) bool { return tables[i] > tables[j] })
	return tables, nil
}

func requestLogTablesSince(tables []string, since time.Time, location *time.Location) []string {
	if location == nil {
		location = time.Local
	}
	sinceDay := since.In(location).Format("20060102")
	out := make([]string, 0, len(tables))
	for _, table := range tables {
		day := strings.TrimPrefix(table, "request_logs_")
		if day >= sinceDay {
			out = append(out, table)
		}
	}
	return out
}

func (r *Repository) usageSummaryFromRequestLogs(ctx context.Context, accountID int64, since time.Time, location *time.Location) (model.UsageSummary, error) {
	var summary model.UsageSummary
	if location == nil {
		location = time.Local
	}
	tables, err := r.requestLogTables(ctx)
	if err != nil {
		return summary, err
	}
	tables = requestLogTablesSince(tables, since, location)
	if len(tables) == 0 {
		return summary, nil
	}
	for _, table := range tables {
		if err := ensureRequestLogColumns(ctx, r.db, table); err != nil {
			return summary, err
		}
	}
	unions := make([]string, 0, len(tables))
	args := make([]any, 0, len(tables)*2)
	sinceText := since.UTC().Format(time.RFC3339Nano)
	for _, table := range tables {
		unions = append(unions, fmt.Sprintf(`SELECT input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, total_tokens
			FROM %s WHERE account_id=? AND started_at>=?`, table))
		args = append(args, accountID, sinceText)
	}
	query := `SELECT COUNT(*),
		COALESCE(SUM(input_tokens),0),
		COALESCE(SUM(output_tokens),0),
		COALESCE(SUM(cache_read_tokens),0),
		COALESCE(SUM(cache_creation_tokens),0),
		COALESCE(SUM(CASE WHEN total_tokens IS NOT NULL THEN total_tokens ELSE COALESCE(input_tokens,0)+COALESCE(output_tokens,0) END),0)
		FROM (` + strings.Join(unions, " UNION ALL ") + `)`
	err = r.db.QueryRowContext(ctx, query, args...).Scan(
		&summary.Requests24H,
		&summary.InputTokens24H,
		&summary.OutputTokens24H,
		&summary.CacheReadTokens24H,
		&summary.CacheCreationTokens24H,
		&summary.TotalTokens24H,
	)
	return summary, err
}

func requestLogWhere(filter RequestLogFilter) (string, []any) {
	clauses := []string{"1=1"}
	var args []any
	if filter.AccountID != nil {
		clauses = append(clauses, "account_id=?")
		args = append(args, *filter.AccountID)
	}
	if len(filter.AccountIDs) > 0 {
		placeholders := make([]string, 0, len(filter.AccountIDs))
		for _, id := range filter.AccountIDs {
			placeholders = append(placeholders, "?")
			args = append(args, id)
		}
		clauses = append(clauses, "account_id IN ("+strings.Join(placeholders, ",")+")")
	}
	if filter.APIKeyID != nil {
		clauses = append(clauses, "api_key_id=?")
		args = append(args, *filter.APIKeyID)
	}
	if provider := strings.TrimSpace(filter.Provider); provider != "" {
		clauses = append(clauses, "provider=?")
		args = append(args, provider)
	}
	if filter.StatusCode != nil {
		clauses = append(clauses, "status_code=?")
		args = append(args, *filter.StatusCode)
	}
	if q := strings.TrimSpace(filter.Query); q != "" {
		like := "%" + q + "%"
		clauses = append(clauses, `(IFNULL(path,'') LIKE ? OR IFNULL(query,'') LIKE ? OR IFNULL(client_ip,'') LIKE ? OR IFNULL(model,'') LIKE ? OR IFNULL(request_id,'') LIKE ? OR IFNULL(error_type,'') LIKE ? OR IFNULL(account_name,'') LIKE ? OR IFNULL(api_key_name,'') LIKE ?)`)
		args = append(args, like, like, like, like, like, like, like, like)
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
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
		query TEXT,
		client_ip TEXT,
		status_code INTEGER NOT NULL,
		started_at TEXT NOT NULL,
		finished_at TEXT NOT NULL,
		duration_ms INTEGER NOT NULL,
		request_id TEXT,
		error_type TEXT,
		model TEXT,
		input_tokens INTEGER,
		output_tokens INTEGER,
		cache_read_tokens INTEGER,
		cache_creation_tokens INTEGER,
		total_tokens INTEGER,
		request_headers TEXT,
		request_body TEXT,
		response_headers TEXT,
		response_body TEXT,
		request_truncated INTEGER NOT NULL DEFAULT 0,
		response_truncated INTEGER NOT NULL DEFAULT 0
	)`, table)
	if _, err := tx.ExecContext(ctx, statement); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%s_account_started ON %s(account_id, started_at DESC)`, table, table)); err != nil {
		return err
	}
	return ensureRequestLogColumns(ctx, tx, table)
}

type execQuerier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func ensureRequestLogColumns(ctx context.Context, db execQuerier, table string) error {
	if !requestLogTablePattern.MatchString(table) {
		return fmt.Errorf("invalid request log table name %q", table)
	}
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return err
	}
	existing := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, column := range []struct{ name, def string }{
		{"query", "TEXT"},
		{"client_ip", "TEXT"},
		{"model", "TEXT"},
		{"input_tokens", "INTEGER"},
		{"output_tokens", "INTEGER"},
		{"cache_read_tokens", "INTEGER"},
		{"cache_creation_tokens", "INTEGER"},
		{"total_tokens", "INTEGER"},
		{"request_headers", "TEXT"},
		{"request_body", "TEXT"},
		{"response_headers", "TEXT"},
		{"response_body", "TEXT"},
		{"request_truncated", "INTEGER NOT NULL DEFAULT 0"},
		{"response_truncated", "INTEGER NOT NULL DEFAULT 0"},
	} {
		if existing[column.name] {
			continue
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column.name, column.def)); err != nil {
			return fmt.Errorf("add %s.%s: %w", table, column.name, err)
		}
	}
	return nil
}

type requestLogScanner interface {
	Scan(dest ...any) error
}

func scanRequestLogSummary(s requestLogScanner) (RequestLog, error) {
	var entry RequestLog
	var accountID, apiKeyID, input, output, cacheRead, cacheCreation, total sql.NullInt64
	var accountName, apiKeyName, prefix, provider, query, clientIP, requestID, errorType, model sql.NullString
	var started, finished string
	var requestTrunc, responseTrunc int
	if err := s.Scan(&entry.Day, &entry.ID, &accountID, &accountName, &apiKeyID, &apiKeyName, &prefix, &provider,
		&entry.Method, &entry.Path, &query, &clientIP, &entry.StatusCode, &started, &finished, &entry.DurationMs, &requestID, &errorType,
		&model, &input, &output, &cacheRead, &cacheCreation, &total, &requestTrunc, &responseTrunc); err != nil {
		return RequestLog{}, err
	}
	entry.AccountID = nullInt64Ptr(accountID)
	entry.APIKeyID = nullInt64Ptr(apiKeyID)
	entry.AccountName = accountName.String
	entry.APIKeyName = apiKeyName.String
	entry.APIKeyPrefix = prefix.String
	entry.Provider = provider.String
	entry.Query = query.String
	entry.ClientIP = clientIP.String
	entry.RequestID = requestID.String
	entry.ErrorType = errorType.String
	entry.Model = model.String
	entry.InputTokens = nullInt64Ptr(input)
	entry.OutputTokens = nullInt64Ptr(output)
	entry.CacheReadTokens = nullInt64Ptr(cacheRead)
	entry.CacheCreationTokens = nullInt64Ptr(cacheCreation)
	entry.TotalTokens = nullInt64Ptr(total)
	entry.RequestTruncated = requestTrunc == 1
	entry.ResponseTruncated = responseTrunc == 1
	entry.StartedAt, _ = parseLogTime(started)
	entry.FinishedAt, _ = parseLogTime(finished)
	return entry, nil
}

func scanRequestLogDetail(s requestLogScanner) (RequestLog, error) {
	var entry RequestLog
	var accountID, apiKeyID, input, output, cacheRead, cacheCreation, total sql.NullInt64
	var accountName, apiKeyName, prefix, provider, query, clientIP, requestID, errorType, model sql.NullString
	var requestHeaders, requestBody, responseHeaders, responseBody sql.NullString
	var started, finished string
	var requestTrunc, responseTrunc int
	if err := s.Scan(&entry.Day, &entry.ID, &accountID, &accountName, &apiKeyID, &apiKeyName, &prefix, &provider,
		&entry.Method, &entry.Path, &query, &clientIP, &entry.StatusCode, &started, &finished, &entry.DurationMs, &requestID, &errorType,
		&model, &input, &output, &cacheRead, &cacheCreation, &total,
		&requestHeaders, &requestBody, &responseHeaders, &responseBody, &requestTrunc, &responseTrunc); err != nil {
		return RequestLog{}, err
	}
	entry.AccountID = nullInt64Ptr(accountID)
	entry.APIKeyID = nullInt64Ptr(apiKeyID)
	entry.AccountName = accountName.String
	entry.APIKeyName = apiKeyName.String
	entry.APIKeyPrefix = prefix.String
	entry.Provider = provider.String
	entry.Query = query.String
	entry.ClientIP = clientIP.String
	entry.RequestID = requestID.String
	entry.ErrorType = errorType.String
	entry.Model = model.String
	entry.InputTokens = nullInt64Ptr(input)
	entry.OutputTokens = nullInt64Ptr(output)
	entry.CacheReadTokens = nullInt64Ptr(cacheRead)
	entry.CacheCreationTokens = nullInt64Ptr(cacheCreation)
	entry.TotalTokens = nullInt64Ptr(total)
	entry.RequestHeaders = requestHeaders.String
	entry.RequestBody = requestBody.String
	entry.ResponseHeaders = responseHeaders.String
	entry.ResponseBody = responseBody.String
	entry.RequestTruncated = requestTrunc == 1
	entry.ResponseTruncated = responseTrunc == 1
	entry.StartedAt, _ = parseLogTime(started)
	entry.FinishedAt, _ = parseLogTime(finished)
	return entry, nil
}

func parseLogTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02 15:04:05", value)
}

func nullInt64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	v := value.Int64
	return &v
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
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
