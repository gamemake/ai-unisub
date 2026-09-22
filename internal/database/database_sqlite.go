package database

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// SQLiteDatabase is the durable SQLite storage wrapped by MemoryDatabase. It
// stores call traces and proxy logs in one table per UTC calendar day, keeps no
// cache, and executes SQL only: input validation and the record cache belong to
// MemoryDatabase.
type SQLiteDatabase struct {
	mu   sync.Mutex
	path string
	db   *sql.DB
}

// NewSQLiteDatabase creates a SQLite-backed database. The file is created by
// Open; use ":memory:" for a process-local SQLite database.
func NewSQLiteDatabase(path string) *SQLiteDatabase {
	return &SQLiteDatabase{path: path}
}

func (s *SQLiteDatabase) Open() error {
	if s == nil {
		return errors.New("sqlite database is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		return nil
	}
	if s.path != ":memory:" {
		if dir := filepath.Dir(s.path); dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0750); err != nil {
				return fmt.Errorf("create database directory: %w", err)
			}
		}
	}
	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		db.Close()
		return err
	}
	if _, err = db.Exec(sqliteSchema); err != nil {
		db.Close()
		return err
	}
	s.db = db
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS proxy_health (address TEXT NOT NULL, application TEXT NOT NULL, state TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(address,application))`); err != nil {
		db.Close()
		s.db = nil
		return err
	}
	if err = migrateLegacyModuleConfigsSQLite(db); err != nil {
		db.Close()
		s.db = nil
		return err
	}
	return nil
}

func (s *SQLiteDatabase) Close() error {
	if s == nil {
		return errors.New("sqlite database is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

func (s *SQLiteDatabase) ListConfigs() ([]PersistedConfig, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id, type, name, value FROM configs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]PersistedConfig, 0)
	for rows.Next() {
		var value PersistedConfig
		var raw []byte
		if err := rows.Scan(&value.ID, &value.Type, &value.Name, &raw); err != nil {
			return nil, err
		}
		value.Value = slices.Clone(raw)
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *SQLiteDatabase) ListConfigsByType(configType string) ([]PersistedConfig, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	configs, err := s.ListConfigs()
	if err != nil {
		return nil, err
	}
	result := make([]PersistedConfig, 0, len(configs))
	for _, value := range configs {
		if value.Type == configType {
			result = append(result, value)
		}
	}
	return result, nil
}

func (s *SQLiteDatabase) LoadConfig(configType, name string) (PersistedConfig, error) {
	if err := s.ensureOpen(); err != nil {
		return PersistedConfig{}, err
	}
	var value PersistedConfig
	var raw []byte
	err := s.db.QueryRow(`SELECT id, type, name, value FROM configs WHERE type = ? AND name = ?`, configType, name).Scan(&value.ID, &value.Type, &value.Name, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return PersistedConfig{}, nil
	}
	if err != nil {
		return PersistedConfig{}, err
	}
	value.Value = slices.Clone(raw)
	return value, nil
}

func (s *SQLiteDatabase) SaveConfig(value *PersistedConfig) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	result, err := s.db.Exec(
		`INSERT INTO configs(id, type, name, value) VALUES(?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET value=excluded.value`,
		databaseID(value.ID), value.Type, value.Name, string(value.Value),
	)
	if err != nil {
		return err
	}
	if value.ID == 0 {
		id, err := result.LastInsertId()
		if err != nil {
			return err
		}
		value.ID = int(id)
	}
	return nil
}

func (s *SQLiteDatabase) DeleteConfig(id int) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM configs WHERE id = ?`, id)
	return err
}

func (s *SQLiteDatabase) ListProxyGroups() ([]PersistedProxyGroup, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	return queryProxyGroups(s.db)
}

func (s *SQLiteDatabase) SaveProxyGroup(value *PersistedProxyGroup) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	config, err := json.Marshal(value.Config)
	if err != nil {
		return err
	}
	state, err := json.Marshal(value.State)
	if err != nil {
		return err
	}
	result, err := s.db.Exec(`INSERT INTO proxy_groups(id, config, state, created_at, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET config=excluded.config, state=excluded.state, created_at=excluded.created_at, updated_at=excluded.updated_at`, databaseID(value.ID), config, state, value.CreatedAt.UTC(), value.UpdatedAt.UTC())
	if err != nil {
		return err
	}
	if value.ID == 0 {
		id, err := result.LastInsertId()
		if err != nil {
			return err
		}
		value.ID = int(id)
	}
	return nil
}

func (s *SQLiteDatabase) DeleteProxyGroup(id int) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM proxy_groups WHERE id = ?`, id)
	return err
}

func (s *SQLiteDatabase) RecordProxyLog(value *PersistedProxyLog) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	table := proxyLogTable(value.Time)
	if _, err := s.db.Exec(createProxyLogTableSQL(table)); err != nil {
		return err
	}
	_, err := s.db.Exec(fmt.Sprintf("INSERT INTO %s(group_id, proxy_url, url, app_type, http_error_code, http_error_message, time) VALUES(?, ?, ?, ?, ?, ?, ?)", table), value.GroupID, value.ProxyURL, value.URL, value.AppType, value.HTTPErrorCode, value.HTTPErrorMessage, value.Time.UTC())
	return err
}

func (s *SQLiteDatabase) QueryProxyLogs(filter ProxyLogFilter, page, pageSize int) ([]PersistedProxyLog, int, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'proxy_logs_%'`)
	if err != nil {
		return nil, 0, err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, 0, err
		}
		if proxyLogTablePattern.MatchString(name) && proxyLogTableInRange(name, &filter.TimeRange) {
			tables = append(tables, name)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, 0, err
	}
	rows.Close()
	if len(tables) == 0 {
		return []PersistedProxyLog{}, 0, nil
	}
	slices.Sort(tables)
	queries := make([]string, len(tables))
	args := make([]any, 0, len(tables)*6)
	for i, table := range tables {
		queries[i] = fmt.Sprintf("SELECT group_id, proxy_url, url, app_type, http_error_code, http_error_message, time FROM %s WHERE group_id = ? AND (? = '' OR proxy_url = ?) AND (? = '' OR app_type = ?) AND time >= ? AND time < ?", table)
		args = append(args, filter.GroupID, filter.ProxyURL, filter.ProxyURL, filter.AppType, filter.AppType, filter.TimeRange.Start.UTC(), filter.TimeRange.End.UTC())
	}
	query := "SELECT group_id, proxy_url, url, app_type, http_error_code, http_error_message, time FROM (" + strings.Join(queries, " UNION ALL ") + ") ORDER BY time DESC"
	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM ("+query+")", args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	queryArgs := append(slices.Clone(args), pageSize, offset)
	rows, err = s.db.Query(query+" LIMIT ? OFFSET ?", queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	result := []PersistedProxyLog{}
	for rows.Next() {
		var value PersistedProxyLog
		if err := rows.Scan(&value.GroupID, &value.ProxyURL, &value.URL, &value.AppType, &value.HTTPErrorCode, &value.HTTPErrorMessage, &value.Time); err != nil {
			return nil, 0, err
		}
		result = append(result, value)
	}
	return result, total, rows.Err()
}

// CleanupProxyLog removes complete daily proxy log tables older than days. A
// value of zero removes all tables before today and keeps today's logs.
func (s *SQLiteDatabase) CleanupProxyLog(days int) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format("20060102")
	rows, err := s.db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'proxy_logs_%'`)
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			rows.Close()
			return err
		}
		if proxyLogTablePattern.MatchString(table) && strings.TrimPrefix(table, "proxy_logs_") < cutoff {
			tables = append(tables, table)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, table := range tables {
		if _, err := s.db.Exec("DROP TABLE " + table); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteDatabase) LoadCredential(id string) (json.RawMessage, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	var raw []byte
	if err := s.db.QueryRow(`SELECT credential FROM oauth_credentials WHERE id = ?`, id).Scan(&raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func (s *SQLiteDatabase) SaveCredential(id string, value json.RawMessage) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO oauth_credentials(id, credential) VALUES(?, ?) ON CONFLICT(id) DO UPDATE SET credential=excluded.credential`, id, value)
	return err
}

func (s *SQLiteDatabase) DeleteCredential(id string) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM oauth_credentials WHERE id = ?`, id)
	return err
}

func (s *SQLiteDatabase) ListAccounts() ([]PersistedAccount, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	return queryAccounts(s.db)
}

func (s *SQLiteDatabase) SaveAccount(value *PersistedAccount) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	config, err := json.Marshal(value.Config)
	if err != nil {
		return err
	}
	state, err := json.Marshal(value.State)
	if err != nil {
		return err
	}
	quota, err := json.Marshal(value.Quota)
	if err != nil {
		return err
	}
	result, err := s.db.Exec(`INSERT INTO accounts(id, provider, name, config, state, quota, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET provider=excluded.provider, name=excluded.name,
		config=excluded.config, state=excluded.state, quota=excluded.quota, created_at=excluded.created_at, updated_at=excluded.updated_at`,
		databaseID(value.ID), value.AIProvider, value.Name, config, state, quota, value.CreatedAt.UTC(), value.UpdatedAt.UTC())
	if err != nil {
		return err
	}
	if value.ID == 0 {
		id, err := result.LastInsertId()
		if err != nil {
			return err
		}
		value.ID = int(id)
	}
	return nil
}

func (s *SQLiteDatabase) DeleteAccount(id int) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM accounts WHERE id = ?`, id)
	return err
}

func (s *SQLiteDatabase) ListUsers() ([]PersistedUser, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	return queryUsers(s.db)
}

func (s *SQLiteDatabase) SaveUser(value *PersistedUser) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	labels, err := json.Marshal(value.Labels)
	if err != nil {
		return err
	}
	result, err := s.db.Exec(`INSERT INTO users(id, name, labels, role, enabled, password_hash, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, labels=excluded.labels,
		role=excluded.role, enabled=excluded.enabled, password_hash=excluded.password_hash, created_at=excluded.created_at, updated_at=excluded.updated_at`,
		databaseID(value.ID), value.Name, labels, value.Role, value.Enabled, value.PasswordHash, value.CreatedAt.UTC(), value.UpdatedAt.UTC())
	if err != nil {
		return err
	}
	if value.ID == 0 {
		id, err := result.LastInsertId()
		if err != nil {
			return err
		}
		value.ID = int(id)
	}
	return nil
}

func (s *SQLiteDatabase) DeleteUser(id int) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, id)
	return err
}

func (s *SQLiteDatabase) ListAPIKeys(userID int) ([]PersistedAPIKey, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	keys, err := queryAPIKeys(s.db)
	if err != nil {
		return nil, err
	}
	if userID == 0 {
		return keys, nil
	}
	result := make([]PersistedAPIKey, 0, len(keys))
	for _, value := range keys {
		if value.UserID == userID {
			result = append(result, value)
		}
	}
	return result, nil
}

func (s *SQLiteDatabase) SaveAPIKey(value *PersistedAPIKey) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	config, err := json.Marshal(value.Config)
	if err != nil {
		return err
	}
	result, err := s.db.Exec(`INSERT INTO api_keys(id, user_id, account_id, name, key_value, config, valid_seconds, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET user_id=excluded.user_id,
		account_id=excluded.account_id, name=excluded.name, key_value=excluded.key_value,
		config=excluded.config, valid_seconds=excluded.valid_seconds,
		created_at=excluded.created_at, updated_at=excluded.updated_at`,
		databaseID(value.ID), value.UserID, value.AccountID, value.Name, value.Key, config, value.ValidSeconds, value.CreatedAt.UTC(), value.UpdatedAt.UTC())
	if err != nil {
		return err
	}
	if value.ID == 0 {
		id, err := result.LastInsertId()
		if err != nil {
			return err
		}
		value.ID = int(id)
	}
	return nil
}

func (s *SQLiteDatabase) DeleteAPIKey(id int) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM api_keys WHERE id = ?`, id)
	return err
}

func (s *SQLiteDatabase) RecordCallTrace(trace *PersistedCallTrace) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	table := traceTable(trace.StartedAt)
	if err := s.ensureTraceTable(table); err != nil {
		return err
	}
	return s.insertTrace(table, trace)
}

// GetCallTrace returns the complete trace from the UTC daily table identified
// by startedAt. The list query intentionally does not load these large fields.
func (s *SQLiteDatabase) GetCallTrace(startedAt time.Time, id int) (*PersistedCallTrace, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	table := traceTable(startedAt)
	if err := s.ensureTraceTable(table); err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`SELECT id, apikey, provider_type, account_id, request_id, session_id, source_ip, url, outbound_url, http_error_code, http_error_info, original_request_headers, outbound_request_headers, request_body, response_headers, response_body, request_bytes, response_bytes, model, input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, started_at, finished_at FROM %s WHERE id = ?`, table)
	var trace PersistedCallTrace
	var original, outbound, response []byte
	err := s.db.QueryRow(query, id).Scan(&trace.ID, &trace.APIKey, &trace.AIProviderType, &trace.AccountID, &trace.RequestID, &trace.SessionID, &trace.SourceIP, &trace.URL, &trace.OutboundURL, &trace.HTTPErrorCode, &trace.HTTPErrorInfo, &original, &outbound, &trace.RequestBody, &response, &trace.ResponseBody, &trace.RequestBytes, &trace.ResponseBytes, &trace.Model, &trace.InputTokens, &trace.OutputTokens, &trace.CacheCreationTokens, &trace.CacheReadTokens, &trace.StartedAt, &trace.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCallTraceNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(original, &trace.OriginalRequestHeaders); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(outbound, &trace.OutboundRequestHeaders); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(response, &trace.ResponseHeaders); err != nil {
		return nil, err
	}
	return &trace, nil
}

// CleanupCallTrace removes complete daily CallTrace tables older than days. A value of
// zero removes all tables before today and keeps today's traces.
func (s *SQLiteDatabase) CleanupCallTrace(days int) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format("20060102")
	rows, err := s.db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'call_traces_%'`)
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			rows.Close()
			return err
		}
		if traceTablePattern.MatchString(table) && strings.TrimPrefix(table, "call_traces_") < cutoff {
			tables = append(tables, table)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, table := range tables {
		if _, err := s.db.Exec("DROP TABLE " + table); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteDatabase) QueryCallTraces(filter CallTraceFilter, page, pageSize int) ([]PersistedCallTraceSummary, int, error) {
	values := []string{}
	if filter.Search != "" {
		values = append(values, filter.Search)
	}
	return s.queryCallTraces("", nil, page, pageSize, &filter.TimeRange, filter, values...)
}

func (s *SQLiteDatabase) QueryAccountUsage(timeRange TimeRange) ([]AccountUsageRow, UsageTotals, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, UsageTotals{}, err
	}
	tables, err := s.callTraceTablesInRange(timeRange)
	if err != nil {
		return nil, UsageTotals{}, err
	}
	if len(tables) == 0 {
		return []AccountUsageRow{}, UsageTotals{}, nil
	}
	union, args := buildUsageUnionQuery(tables, timeRange, 0)
	query := `SELECT account_id,
		COUNT(*) AS requests,
		COALESCE(SUM(input_tokens), 0),
		COALESCE(SUM(output_tokens), 0),
		COALESCE(SUM(cache_creation_tokens), 0),
		COALESCE(SUM(cache_read_tokens), 0)
	FROM (` + union + `) GROUP BY account_id ORDER BY account_id`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, UsageTotals{}, err
	}
	defer rows.Close()
	result := make([]AccountUsageRow, 0)
	totals := UsageTotals{}
	for rows.Next() {
		var row AccountUsageRow
		if err := rows.Scan(&row.AccountID, &row.Usage.Requests, &row.Usage.InputTokens, &row.Usage.OutputTokens, &row.Usage.CacheCreationTokens, &row.Usage.CacheReadTokens); err != nil {
			return nil, UsageTotals{}, err
		}
		row.Usage.Finalize()
		totals.add(row.Usage)
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, UsageTotals{}, err
	}
	return result, totals, nil
}

func (s *SQLiteDatabase) QueryUserUsage(timeRange TimeRange, accountID int) ([]UserUsageRow, UsageTotals, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, UsageTotals{}, err
	}
	tables, err := s.callTraceTablesInRange(timeRange)
	if err != nil {
		return nil, UsageTotals{}, err
	}
	if len(tables) == 0 {
		return []UserUsageRow{}, UsageTotals{}, nil
	}
	union, args := buildUsageUnionQuery(tables, timeRange, accountID)
	// Match apikey to key_value or stringified key id (historical gateway variants).
	query := `SELECT COALESCE(k.user_id, 0) AS user_id,
		COUNT(*) AS requests,
		COALESCE(SUM(t.input_tokens), 0),
		COALESCE(SUM(t.output_tokens), 0),
		COALESCE(SUM(t.cache_creation_tokens), 0),
		COALESCE(SUM(t.cache_read_tokens), 0)
	FROM (` + union + `) AS t
	LEFT JOIN api_keys k ON (k.key_value = t.apikey OR CAST(k.id AS TEXT) = t.apikey)
	GROUP BY COALESCE(k.user_id, 0)
	ORDER BY user_id`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, UsageTotals{}, err
	}
	defer rows.Close()
	result := make([]UserUsageRow, 0)
	totals := UsageTotals{}
	for rows.Next() {
		var row UserUsageRow
		if err := rows.Scan(&row.UserID, &row.Usage.Requests, &row.Usage.InputTokens, &row.Usage.OutputTokens, &row.Usage.CacheCreationTokens, &row.Usage.CacheReadTokens); err != nil {
			return nil, UsageTotals{}, err
		}
		row.Usage.Finalize()
		totals.add(row.Usage)
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, UsageTotals{}, err
	}
	return result, totals, nil
}

func (s *SQLiteDatabase) callTraceTablesInRange(timeRange TimeRange) ([]string, error) {
	rows, err := s.db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'call_traces_%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tables []string
	tr := timeRange
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if traceTablePattern.MatchString(name) && traceTableInRange(name, &tr) {
			tables = append(tables, name)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.Sort(tables)
	return tables, nil
}

func buildUsageUnionQuery(tables []string, timeRange TimeRange, accountID int) (string, []any) {
	parts := make([]string, 0, len(tables))
	args := make([]any, 0, len(tables)*4)
	start, end := timeRange.Start.UTC(), timeRange.End.UTC()
	for _, table := range tables {
		q := fmt.Sprintf(`SELECT account_id, apikey, input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens
			FROM %s WHERE started_at >= ? AND started_at <= ?`, table)
		args = append(args, start, end)
		if accountID > 0 {
			q += ` AND account_id = ?`
			args = append(args, accountID)
		}
		parts = append(parts, q)
	}
	return strings.Join(parts, " UNION ALL "), args
}
func (s *SQLiteDatabase) queryCallTraces(aiProviderName string, httpErrorCode *int, page, pageSize int, timeRange *TimeRange, filter CallTraceFilter, values ...string) ([]PersistedCallTraceSummary, int, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, 0, err
	}
	var startTime, endTime *time.Time
	if timeRange != nil {
		startTime = new(timeRange.Start.UTC())
		endTime = new(timeRange.End.UTC())
	}
	rows, err := s.db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'call_traces_%'`)
	if err != nil {
		return nil, 0, err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return nil, 0, err
		}
		if traceTablePattern.MatchString(name) && traceTableInRange(name, timeRange) {
			tables = append(tables, name)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, 0, err
	}
	rows.Close()
	if len(tables) == 0 {
		return []PersistedCallTraceSummary{}, 0, nil
	}
	for _, table := range tables {
		if err := s.ensureTraceTable(table); err != nil {
			return nil, 0, err
		}
	}

	unionQuery, args := buildTraceUnionQuery(tables, aiProviderName, httpErrorCode, startTime, endTime, values, filter)
	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM ("+unionQuery+")", args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	query := "SELECT t.id, t.apikey, t.provider_type, t.account_id, t.request_id, t.session_id, t.source_ip, t.url, t.outbound_url, t.http_error_code, t.http_error_info, t.model, t.input_tokens, t.output_tokens, t.cache_creation_tokens, t.cache_read_tokens, t.started_at, t.finished_at, COALESCE((SELECT u.name FROM api_keys k JOIN users u ON u.id = k.user_id WHERE (k.key_value = t.apikey OR k.id = t.apikey) LIMIT 1), '') FROM (" + unionQuery + ") AS t ORDER BY t.started_at DESC, t.id DESC LIMIT ? OFFSET ?"
	queryArgs := append(slices.Clone(args), pageSize, offset)
	rows, err = s.db.Query(query, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	result := make([]PersistedCallTraceSummary, 0, pageSize)
	for rows.Next() {
		var trace PersistedCallTraceSummary
		if err := rows.Scan(&trace.ID, &trace.APIKey, &trace.AIProviderType, &trace.AccountID, &trace.RequestID, &trace.SessionID, &trace.SourceIP, &trace.URL, &trace.OutboundURL, &trace.HTTPErrorCode, &trace.HTTPErrorInfo, &trace.Model, &trace.InputTokens, &trace.OutputTokens, &trace.CacheCreationTokens, &trace.CacheReadTokens, &trace.StartedAt, &trace.FinishedAt, &trace.Username); err != nil {
			return nil, 0, err
		}
		result = append(result, trace)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return result, total, nil
}

func (s *SQLiteDatabase) ensureOpen() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("sqlite database is not open")
	}
	return nil
}

func (s *SQLiteDatabase) insertTrace(table string, t *PersistedCallTrace) error {
	original, _ := json.Marshal(t.OriginalRequestHeaders)
	outbound, _ := json.Marshal(t.OutboundRequestHeaders)
	response, _ := json.Marshal(t.ResponseHeaders)
	result, err := s.db.Exec(fmt.Sprintf(`INSERT OR REPLACE INTO %s(id, apikey, provider_type, account_id, request_id, session_id, source_ip, url, outbound_url, http_error_code, http_error_info, original_request_headers, outbound_request_headers, request_body, response_headers, response_body, request_bytes, response_bytes, model, input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, started_at, finished_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, table), databaseID(t.ID), t.APIKey, t.AIProviderType, t.AccountID, t.RequestID, t.SessionID, t.SourceIP, t.URL, t.OutboundURL, t.HTTPErrorCode, t.HTTPErrorInfo, original, outbound, t.RequestBody, response, t.ResponseBody, t.RequestBytes, t.ResponseBytes, t.Model, t.InputTokens, t.OutputTokens, t.CacheCreationTokens, t.CacheReadTokens, t.StartedAt.UTC(), t.FinishedAt.UTC())
	if err == nil && t.ID == 0 {
		id, idErr := result.LastInsertId()
		if idErr != nil {
			return idErr
		}
		t.ID = int(id)
	}
	return err
}

var traceTablePattern = regexp.MustCompile(`^call_traces_[0-9]{8}$`)

func traceTable(t time.Time) string { return "call_traces_" + t.UTC().Format("20060102") }

func traceTableInRange(table string, timeRange *TimeRange) bool {
	if timeRange == nil {
		return true
	}
	tableDate := strings.TrimPrefix(table, "call_traces_")
	startDate := timeRange.Start.UTC().Format("20060102")
	endDate := timeRange.End.UTC().Format("20060102")
	return tableDate >= startDate && tableDate <= endDate
}

func createTraceTableSQL(table string) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (id INTEGER PRIMARY KEY AUTOINCREMENT, apikey TEXT, provider_type TEXT, account_id INTEGER, request_id TEXT, session_id TEXT NOT NULL DEFAULT '', source_ip TEXT, url TEXT, outbound_url TEXT NOT NULL DEFAULT '', http_error_code INTEGER, http_error_info TEXT, original_request_headers BLOB, outbound_request_headers BLOB, request_body BLOB, response_headers BLOB, response_body BLOB, request_bytes INTEGER NOT NULL DEFAULT 0, response_bytes INTEGER NOT NULL DEFAULT 0, model TEXT, input_tokens INTEGER, output_tokens INTEGER, cache_creation_tokens INTEGER, cache_read_tokens INTEGER, started_at DATETIME, finished_at DATETIME)`, table)
}

func createTraceIndexesSQL(table string) string {
	return fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%s_started_at ON %s(started_at);
CREATE INDEX IF NOT EXISTS idx_%s_http_error_code ON %s(http_error_code);
CREATE INDEX IF NOT EXISTS idx_%s_account_id ON %s(account_id);
CREATE INDEX IF NOT EXISTS idx_%s_apikey ON %s(apikey);
CREATE INDEX IF NOT EXISTS idx_%s_source_ip ON %s(source_ip);
CREATE INDEX IF NOT EXISTS idx_%s_model ON %s(model);
CREATE INDEX IF NOT EXISTS idx_%s_request_id ON %s(request_id);`, table, table, table, table, table, table, table, table, table, table, table, table, table, table)
}

// ensureTraceTable creates a daily call-trace table when missing and applies
// additive column upgrades so older daily tables keep working.
func (s *SQLiteDatabase) ensureTraceTable(table string) error {
	if !traceTablePattern.MatchString(table) {
		return errors.New("invalid call trace table name")
	}
	if _, err := s.db.Exec(createTraceTableSQL(table)); err != nil {
		return err
	}
	// Upgrade missing columns before indexes that depend on them.
	if err := s.ensureTraceColumns(table); err != nil {
		return err
	}
	if _, err := s.db.Exec(createTraceIndexesSQL(table)); err != nil {
		return err
	}
	if _, err := s.db.Exec("CREATE INDEX IF NOT EXISTS idx_" + table + "_session_id ON " + table + "(session_id)"); err != nil {
		return err
	}
	return nil
}

func (s *SQLiteDatabase) ensureTraceColumns(table string) error {
	columns, err := s.tableColumns(table)
	if err != nil {
		return err
	}
	// session_id was added after early call-trace tables; outbound_url is newer.
	if _, ok := columns["session_id"]; !ok {
		if _, err := s.db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN session_id TEXT NOT NULL DEFAULT ''`, table)); err != nil {
			return err
		}
	}
	if _, ok := columns["outbound_url"]; !ok {
		if _, err := s.db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN outbound_url TEXT NOT NULL DEFAULT ''`, table)); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteDatabase) tableColumns(table string) (map[string]struct{}, error) {
	// PRAGMA identifiers are not parameterized; only allow safe names.
	if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(table) {
		return nil, errors.New("invalid table name")
	}
	rows, err := s.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := make(map[string]struct{})
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return nil, err
		}
		columns[name] = struct{}{}
	}
	return columns, rows.Err()
}

func (s *SQLiteDatabase) queryTraceTable(table, userName, aiProviderName string, code *int, startTime, endTime *time.Time, values []string) ([]PersistedCallTrace, error) {
	if err := s.ensureTraceTable(table); err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`SELECT id, apikey, provider_type, account_id, request_id, session_id, source_ip, url, outbound_url, http_error_code, http_error_info, original_request_headers, outbound_request_headers, request_body, response_headers, response_body, request_bytes, response_bytes, model, input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, started_at, finished_at FROM %s WHERE 1=1`, table)
	args := []any{}
	if code != nil {
		query += " AND http_error_code = ?"
		args = append(args, *code)
	}
	if startTime != nil {
		query += " AND started_at >= ?"
		args = append(args, startTime.UTC())
	}
	if endTime != nil {
		query += " AND started_at <= ?"
		args = append(args, endTime.UTC())
	}
	if aiProviderName != "" {
		query += " AND account_id IN (SELECT id FROM accounts WHERE name = ? OR provider = ?)"
		args = append(args, aiProviderName, aiProviderName)
	}
	if userName != "" {
		query += " AND EXISTS (SELECT 1 FROM api_keys k JOIN users u ON u.id = k.user_id WHERE (k.key_value = apikey OR k.id = apikey) AND u.name = ?)"
		args = append(args, userName)
	}
	if len(values) > 0 {
		placeholders := make([]string, len(values))
		for i, value := range values {
			placeholders[i] = "?"
			args = append(args, value)
		}
		query += " AND (source_ip IN (" + strings.Join(placeholders, ",") + ") OR model IN (" + strings.Join(placeholders, ",") + ") OR request_id IN (" + strings.Join(placeholders, ",") + "))"
		for _, value := range values {
			args = append(args, value)
		}
		for _, value := range values {
			args = append(args, value)
		}
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []PersistedCallTrace{}
	for rows.Next() {
		var t PersistedCallTrace
		var original, outbound, response []byte
		if err = rows.Scan(&t.ID, &t.APIKey, &t.AIProviderType, &t.AccountID, &t.RequestID, &t.SessionID, &t.SourceIP, &t.URL, &t.OutboundURL, &t.HTTPErrorCode, &t.HTTPErrorInfo, &original, &outbound, &t.RequestBody, &response, &t.ResponseBody, &t.RequestBytes, &t.ResponseBytes, &t.Model, &t.InputTokens, &t.OutputTokens, &t.CacheCreationTokens, &t.CacheReadTokens, &t.StartedAt, &t.FinishedAt); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(original, &t.OriginalRequestHeaders); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(outbound, &t.OutboundRequestHeaders); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(response, &t.ResponseHeaders); err != nil {
			return nil, err
		}
		result = append(result, t)
	}
	return result, rows.Err()
}

func buildTraceUnionQuery(tables []string, aiProviderName string, code *int, startTime, endTime *time.Time, values []string, filters ...CallTraceFilter) (string, []any) {
	filter := CallTraceFilter{}
	if len(filters) > 0 {
		filter = filters[0]
	}
	queries := make([]string, 0, len(tables))
	args := make([]any, 0)
	for _, table := range tables {
		query := fmt.Sprintf(`SELECT id, apikey, provider_type, account_id, request_id, session_id, source_ip, url, outbound_url, http_error_code, http_error_info, model, input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, started_at, finished_at FROM %s WHERE 1=1`, table)
		queryArgs := make([]any, 0)
		appendFilter := func(condition string, filterArgs ...any) {
			query += " AND " + condition
			queryArgs = append(queryArgs, filterArgs...)
		}

		if filter.AccountID != 0 {
			appendFilter("account_id = ?", filter.AccountID)
		}
		if filter.Code != nil {
			appendFilter("http_error_code = ?", *filter.Code)
		}
		if code != nil {
			appendFilter("http_error_code = ?", *code)
		}
		if startTime != nil {
			appendFilter("started_at >= ?", startTime.UTC())
		}
		if endTime != nil {
			appendFilter("started_at <= ?", endTime.UTC())
		}
		if aiProviderName != "" {
			appendFilter("account_id IN (SELECT id FROM accounts WHERE name = ? OR provider = ?)", aiProviderName, aiProviderName)
		}
		if filter.UserID != nil {
			if *filter.UserID == 0 {
				appendFilter("NOT EXISTS (SELECT 1 FROM api_keys k WHERE k.key_value = apikey OR k.id = apikey)")
			} else {
				appendFilter("EXISTS (SELECT 1 FROM api_keys k WHERE (k.key_value = apikey OR k.id = apikey) AND k.user_id = ?)", *filter.UserID)
			}
		}
		if len(values) > 0 {
			placeholders := make([]string, len(values))
			for i := range values {
				placeholders[i] = "?"
			}
			condition := "(source_ip IN (" + strings.Join(placeholders, ",") + ") OR model IN (" + strings.Join(placeholders, ",") + ") OR request_id IN (" + strings.Join(placeholders, ",") + ") OR session_id IN (" + strings.Join(placeholders, ",") + ")"
			searchArgs := valuesToAny(values)
			if filter.SearchUsernames {
				condition += " OR EXISTS (SELECT 1 FROM api_keys k JOIN users u ON u.id=k.user_id WHERE (k.key_value=apikey OR k.id=apikey) AND u.name IN (" + strings.Join(placeholders, ",") + "))"
				for _, v := range values {
					searchArgs = append(searchArgs, v)
				}
			}
			appendFilter(condition+")", searchArgs...)
		}
		queries = append(queries, query)
		args = append(args, queryArgs...)
	}
	return strings.Join(queries, " UNION ALL "), args
}

var proxyLogTablePattern = regexp.MustCompile(`^proxy_logs_[0-9]{8}$`)

func proxyLogTable(t time.Time) string { return "proxy_logs_" + t.UTC().Format("20060102") }

func proxyLogTableInRange(table string, timeRange *TimeRange) bool {
	if timeRange == nil {
		return true
	}
	day := strings.TrimPrefix(table, "proxy_logs_")
	start := timeRange.Start.UTC().Format("20060102")
	end := timeRange.End.UTC().Format("20060102")
	return day >= start && day <= end
}

func createProxyLogTableSQL(table string) string {
	if !proxyLogTablePattern.MatchString(table) {
		panic("invalid proxy log table name")
	}
	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (group_id INTEGER NOT NULL, proxy_url TEXT NOT NULL, url TEXT NOT NULL DEFAULT '', app_type TEXT NOT NULL DEFAULT '', http_error_code INTEGER NOT NULL, http_error_message TEXT NOT NULL, time DATETIME NOT NULL)", table)
}

func valuesToAny(values []string) []any {
	result := make([]any, len(values)*4)
	for i, value := range values {
		result[i] = value
		result[len(values)+i] = value
		result[len(values)*2+i] = value
		result[len(values)*3+i] = value
	}
	return result
}

const sqliteSchema = `
CREATE TABLE IF NOT EXISTS proxy_stats (address TEXT NOT NULL, application TEXT NOT NULL, start_at INTEGER NOT NULL, source TEXT NOT NULL, requests INTEGER NOT NULL, failures INTEGER NOT NULL, PRIMARY KEY(address,application,start_at,source));
CREATE TABLE IF NOT EXISTS oauth_credentials (id TEXT PRIMARY KEY, credential BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS accounts (id INTEGER PRIMARY KEY AUTOINCREMENT, provider TEXT NOT NULL, name TEXT NOT NULL, config BLOB NOT NULL, state BLOB NOT NULL DEFAULT '{}', quota BLOB NOT NULL DEFAULT '{}', created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL);
CREATE INDEX IF NOT EXISTS idx_accounts_name ON accounts(name);
CREATE INDEX IF NOT EXISTS idx_accounts_provider ON accounts(provider);
CREATE TABLE IF NOT EXISTS users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, labels BLOB NOT NULL, role TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, password_hash TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL);
CREATE INDEX IF NOT EXISTS idx_users_name ON users(name);
CREATE TABLE IF NOT EXISTS api_keys (id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE, name TEXT NOT NULL DEFAULT '', key_value TEXT NOT NULL, config BLOB NOT NULL DEFAULT '{}', valid_seconds INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL);
CREATE INDEX IF NOT EXISTS idx_api_keys_user_id ON api_keys(user_id);
CREATE INDEX IF NOT EXISTS idx_api_keys_account_id ON api_keys(account_id);
CREATE INDEX IF NOT EXISTS idx_api_keys_key_value ON api_keys(key_value);
CREATE TABLE IF NOT EXISTS proxy_groups (id INTEGER PRIMARY KEY AUTOINCREMENT, config BLOB NOT NULL DEFAULT '{}', state BLOB NOT NULL DEFAULT '{}', created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL);
CREATE TABLE IF NOT EXISTS configs (id INTEGER PRIMARY KEY AUTOINCREMENT, type TEXT NOT NULL, name TEXT NOT NULL, value TEXT NOT NULL, UNIQUE(type, name));
CREATE INDEX IF NOT EXISTS idx_configs_type ON configs(type);`

func migrateLegacyModuleConfigsSQLite(db *sql.DB) error {
	var exists bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='module_configs')`).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return nil
	}
	_, err := db.Exec(`INSERT OR IGNORE INTO configs(type, name, value)
		SELECT ?, module, config FROM module_configs`, ModuleConfigType)
	return err
}

var _ Database = (*SQLiteDatabase)(nil)
