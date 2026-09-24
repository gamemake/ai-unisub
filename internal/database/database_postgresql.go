package database

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// PostgreSQLDatabase is the durable PostgreSQL storage wrapped by
// MemoryDatabase. It stores every entity in PostgreSQL tables and the
// high-volume records in RANGE-partitioned tables; partitions are created per
// UTC day and remain native PostgreSQL partitions rather than independent
// application-managed tables. It keeps no cache and executes SQL only: input
// validation and the record cache belong to MemoryDatabase.
type PostgreSQLDatabase struct {
	mu          sync.Mutex
	partitionMu sync.Mutex
	url         string
	db          *sql.DB
}

// NewPostgreSQLDatabase creates a PostgreSQL-backed database. The connection
// is established by Open.
func NewPostgreSQLDatabase(databaseURL string) *PostgreSQLDatabase {
	return &PostgreSQLDatabase{url: databaseURL}
}

func (p *PostgreSQLDatabase) Open() error {
	if p == nil {
		return errors.New("postgresql database is nil")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.db != nil {
		return nil
	}
	db, err := sql.Open("pgx", p.url)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(10)
	for _, statement := range postgresSchema {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			return fmt.Errorf("initialize PostgreSQL schema: %w", err)
		}
	}
	for _, statement := range []string{
		`ALTER TABLE call_traces ADD COLUMN IF NOT EXISTS user_id BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE call_traces ADD COLUMN IF NOT EXISTS request_method TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE call_traces ADD COLUMN IF NOT EXISTS queue_duration_ms BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE call_traces ADD COLUMN IF NOT EXISTS request_duration_ms BIGINT NOT NULL DEFAULT 0`,
	} {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			return fmt.Errorf("migrate PostgreSQL call trace schema: %w", err)
		}
	}
	if err := migrateLegacyModuleConfigsPostgreSQL(db); err != nil {
		_ = db.Close()
		return err
	}
	p.db = db
	return nil
}

func (p *PostgreSQLDatabase) Close() error {
	if p == nil {
		return errors.New("postgresql database is nil")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.db == nil {
		return nil
	}
	err := p.db.Close()
	p.db = nil
	return err
}

func (p *PostgreSQLDatabase) ListConfigs() ([]PersistedConfig, error) {
	if err := p.ensureOpen(); err != nil {
		return nil, err
	}
	rows, err := p.db.Query(`SELECT id, type, name, value FROM configs ORDER BY id`)
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

func (p *PostgreSQLDatabase) ListConfigsByType(configType string) ([]PersistedConfig, error) {
	if err := p.ensureOpen(); err != nil {
		return nil, err
	}
	configs, err := p.ListConfigs()
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

func (p *PostgreSQLDatabase) LoadConfig(configType, name string) (PersistedConfig, error) {
	if err := p.ensureOpen(); err != nil {
		return PersistedConfig{}, err
	}
	var value PersistedConfig
	var raw []byte
	err := p.db.QueryRow(`SELECT id, type, name, value FROM configs WHERE type=$1 AND name=$2`, configType, name).Scan(&value.ID, &value.Type, &value.Name, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return PersistedConfig{}, nil
	}
	if err != nil {
		return PersistedConfig{}, err
	}
	value.Value = slices.Clone(raw)
	return value, nil
}

func (p *PostgreSQLDatabase) SaveConfig(value *PersistedConfig) error {
	if err := p.ensureOpen(); err != nil {
		return err
	}
	var err error
	if value.ID == 0 {
		err = p.db.QueryRow(`INSERT INTO configs(type, name, value) VALUES($1, $2, $3::jsonb) RETURNING id`, value.Type, value.Name, string(value.Value)).Scan(&value.ID)
	} else {
		err = p.db.QueryRow(`INSERT INTO configs(id, type, name, value) VALUES($1, $2, $3, $4::jsonb)
		ON CONFLICT(id) DO UPDATE SET value=excluded.value RETURNING id`, value.ID, value.Type, value.Name, string(value.Value)).Scan(&value.ID)
	}
	if err != nil {
		return err
	}
	return nil
}

func (p *PostgreSQLDatabase) DeleteConfig(id int) error {
	if err := p.ensureOpen(); err != nil {
		return err
	}
	_, err := p.db.Exec(`DELETE FROM configs WHERE id=$1`, id)
	return err
}

func (p *PostgreSQLDatabase) ListProxyGroups() ([]PersistedProxyGroup, error) {
	if err := p.ensureOpen(); err != nil {
		return nil, err
	}
	return queryProxyGroups(p.db)
}

func (p *PostgreSQLDatabase) SaveProxyGroup(value *PersistedProxyGroup) error {
	if err := p.ensureOpen(); err != nil {
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
	if value.ID == 0 {
		err = p.db.QueryRow(`INSERT INTO proxy_groups(config, state, created_at, updated_at)
			VALUES($1::jsonb, $2::jsonb, $3, $4) RETURNING id`, string(config), string(state), value.CreatedAt.UTC(), value.UpdatedAt.UTC()).Scan(&value.ID)
	} else {
		err = p.db.QueryRow(`INSERT INTO proxy_groups(id, config, state, created_at, updated_at)
			VALUES($1, $2::jsonb, $3::jsonb, $4, $5)
			ON CONFLICT(id) DO UPDATE SET config=excluded.config, state=excluded.state,
			created_at=excluded.created_at, updated_at=excluded.updated_at RETURNING id`, value.ID, string(config), string(state), value.CreatedAt.UTC(), value.UpdatedAt.UTC()).Scan(&value.ID)
	}
	if err != nil {
		return err
	}
	return nil
}

func (p *PostgreSQLDatabase) DeleteProxyGroup(id int) error {
	if err := p.ensureOpen(); err != nil {
		return err
	}
	_, err := p.db.Exec(`DELETE FROM proxy_groups WHERE id=$1`, id)
	return err
}

func (p *PostgreSQLDatabase) RecordProxyLog(value *PersistedProxyLog) error {
	if err := p.ensureOpen(); err != nil {
		return err
	}
	if err := p.ensurePartition("proxy_logs", value.Time); err != nil {
		return err
	}
	_, err := p.db.Exec(`INSERT INTO proxy_logs(group_id, proxy_url, url, app_type, http_error_code, http_error_message, time)
		VALUES($1, $2, $3, $4, $5, $6, $7)`, value.GroupID, value.ProxyURL, value.URL, value.AppType, value.HTTPErrorCode, value.HTTPErrorMessage, value.Time.UTC())
	return err
}

func (p *PostgreSQLDatabase) QueryProxyLogs(filter ProxyLogFilter, page, pageSize int) ([]PersistedProxyLog, int, error) {
	if err := p.ensureOpen(); err != nil {
		return nil, 0, err
	}
	const condition = `FROM proxy_logs WHERE group_id=$1 AND ($2='' OR proxy_url=$2)
		AND ($3='' OR app_type=$3) AND time >= $4 AND time < $5`
	var total int
	if err := p.db.QueryRow(`SELECT COUNT(*) `+condition, filter.GroupID, filter.ProxyURL, filter.AppType, filter.TimeRange.Start.UTC(), filter.TimeRange.End.UTC()).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := p.db.Query(`SELECT group_id, proxy_url, url, app_type, http_error_code, http_error_message, time `+condition+`
		ORDER BY time DESC LIMIT $6 OFFSET $7`, filter.GroupID, filter.ProxyURL, filter.AppType, filter.TimeRange.Start.UTC(), filter.TimeRange.End.UTC(), pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	result := make([]PersistedProxyLog, 0)
	for rows.Next() {
		var value PersistedProxyLog
		if err := rows.Scan(&value.GroupID, &value.ProxyURL, &value.URL, &value.AppType, &value.HTTPErrorCode, &value.HTTPErrorMessage, &value.Time); err != nil {
			return nil, 0, err
		}
		result = append(result, value)
	}
	return result, total, rows.Err()
}

func (p *PostgreSQLDatabase) CleanupProxyLog(days int) error {
	if err := p.ensureOpen(); err != nil {
		return err
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format("20060102")
	partitions, err := p.listPartitions("proxy_logs")
	if err != nil {
		return err
	}
	for _, partition := range partitions {
		if proxyPartitionPattern.MatchString(partition) && strings.TrimPrefix(partition, "proxy_logs_") < cutoff {
			if _, err := p.db.Exec("DROP TABLE " + quoteIdentifier(partition)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *PostgreSQLDatabase) LoadCredential(id string) (json.RawMessage, error) {
	if err := p.ensureOpen(); err != nil {
		return nil, err
	}
	var raw []byte
	if err := p.db.QueryRow(`SELECT credential FROM oauth_credentials WHERE id=$1`, id).Scan(&raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func (p *PostgreSQLDatabase) SaveCredential(id string, value json.RawMessage) error {
	if err := p.ensureOpen(); err != nil {
		return err
	}
	if _, err := p.db.Exec(`INSERT INTO oauth_credentials(id, credential) VALUES($1, $2::jsonb)
		ON CONFLICT(id) DO UPDATE SET credential=excluded.credential`, id, string(value)); err != nil {
		return err
	}
	return nil
}

func (p *PostgreSQLDatabase) DeleteCredential(id string) error {
	if err := p.ensureOpen(); err != nil {
		return err
	}
	_, err := p.db.Exec(`DELETE FROM oauth_credentials WHERE id=$1`, id)
	return err
}

func (p *PostgreSQLDatabase) ListAccounts() ([]PersistedAccount, error) {
	if err := p.ensureOpen(); err != nil {
		return nil, err
	}
	return queryAccounts(p.db)
}

func (p *PostgreSQLDatabase) SaveAccount(value *PersistedAccount) error {
	if err := p.ensureOpen(); err != nil {
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
	if value.ID == 0 {
		err = p.db.QueryRow(`INSERT INTO accounts(provider, name, config, state, quota, created_at, updated_at)
			VALUES($1, $2, $3::jsonb, $4::jsonb, $5::jsonb, $6, $7) RETURNING id`, value.AIProvider, value.Name, string(config), string(state), string(quota), value.CreatedAt.UTC(), value.UpdatedAt.UTC()).Scan(&value.ID)
	} else {
		err = p.db.QueryRow(`INSERT INTO accounts(id, provider, name, config, state, quota, created_at, updated_at)
			VALUES($1, $2, $3, $4::jsonb, $5::jsonb, $6::jsonb, $7, $8)
			ON CONFLICT(id) DO UPDATE SET provider=excluded.provider, name=excluded.name,
			config=excluded.config, state=excluded.state, quota=excluded.quota,
			created_at=excluded.created_at, updated_at=excluded.updated_at RETURNING id`, value.ID, value.AIProvider, value.Name, string(config), string(state), string(quota), value.CreatedAt.UTC(), value.UpdatedAt.UTC()).Scan(&value.ID)
	}
	if err != nil {
		return err
	}
	return nil
}

func (p *PostgreSQLDatabase) DeleteAccount(id int) error {
	if err := p.ensureOpen(); err != nil {
		return err
	}
	_, err := p.db.Exec(`DELETE FROM accounts WHERE id=$1`, id)
	return err
}

func (p *PostgreSQLDatabase) ListUsers() ([]PersistedUser, error) {
	if err := p.ensureOpen(); err != nil {
		return nil, err
	}
	return queryUsers(p.db)
}

func (p *PostgreSQLDatabase) SaveUser(value *PersistedUser) error {
	if err := p.ensureOpen(); err != nil {
		return err
	}
	labels, err := json.Marshal(value.Labels)
	if err != nil {
		return err
	}
	if value.ID == 0 {
		err = p.db.QueryRow(`INSERT INTO users(name, labels, role, enabled, password_hash, created_at, updated_at)
			VALUES($1, $2::jsonb, $3, $4, $5, $6, $7) RETURNING id`, value.Name, string(labels), value.Role, value.Enabled, value.PasswordHash, value.CreatedAt.UTC(), value.UpdatedAt.UTC()).Scan(&value.ID)
	} else {
		err = p.db.QueryRow(`INSERT INTO users(id, name, labels, role, enabled, password_hash, created_at, updated_at)
			VALUES($1, $2, $3::jsonb, $4, $5, $6, $7, $8)
			ON CONFLICT(id) DO UPDATE SET name=excluded.name, labels=excluded.labels,
			role=excluded.role, enabled=excluded.enabled, password_hash=excluded.password_hash,
			created_at=excluded.created_at, updated_at=excluded.updated_at RETURNING id`, value.ID, value.Name, string(labels), value.Role, value.Enabled, value.PasswordHash, value.CreatedAt.UTC(), value.UpdatedAt.UTC()).Scan(&value.ID)
	}
	if err != nil {
		return err
	}
	return nil
}

func (p *PostgreSQLDatabase) DeleteUser(id int) error {
	if err := p.ensureOpen(); err != nil {
		return err
	}
	_, err := p.db.Exec(`DELETE FROM users WHERE id=$1`, id)
	return err
}

func (p *PostgreSQLDatabase) ListAPIKeys(userID int) ([]PersistedAPIKey, error) {
	if err := p.ensureOpen(); err != nil {
		return nil, err
	}
	keys, err := queryAPIKeys(p.db)
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

func (p *PostgreSQLDatabase) SaveAPIKey(value *PersistedAPIKey) error {
	if err := p.ensureOpen(); err != nil {
		return err
	}
	config, err := json.Marshal(value.Config)
	if err != nil {
		return err
	}
	if value.ID == 0 {
		err = p.db.QueryRow(`INSERT INTO api_keys(user_id, account_id, name, key_value, config, valid_seconds, created_at, updated_at)
			VALUES($1, $2, $3, $4, $5::jsonb, $6, $7, $8) RETURNING id`, value.UserID, value.AccountID, value.Name, value.Key, string(config), value.ValidSeconds, value.CreatedAt.UTC(), value.UpdatedAt.UTC()).Scan(&value.ID)
	} else {
		err = p.db.QueryRow(`INSERT INTO api_keys(id, user_id, account_id, name, key_value, config, valid_seconds, created_at, updated_at)
			VALUES($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9)
			ON CONFLICT(id) DO UPDATE SET user_id=excluded.user_id, account_id=excluded.account_id,
			name=excluded.name, key_value=excluded.key_value, config=excluded.config,
			valid_seconds=excluded.valid_seconds, created_at=excluded.created_at,
			updated_at=excluded.updated_at RETURNING id`, value.ID, value.UserID, value.AccountID, value.Name, value.Key, string(config), value.ValidSeconds, value.CreatedAt.UTC(), value.UpdatedAt.UTC()).Scan(&value.ID)
	}
	if err != nil {
		return err
	}
	return nil
}

func (p *PostgreSQLDatabase) DeleteAPIKey(id int) error {
	if err := p.ensureOpen(); err != nil {
		return err
	}
	_, err := p.db.Exec(`DELETE FROM api_keys WHERE id=$1`, id)
	return err
}

func (p *PostgreSQLDatabase) RecordCallTrace(trace *PersistedCallTrace) error {
	if err := p.ensureOpen(); err != nil {
		return err
	}
	finishedAt := trace.FinishedAt
	if finishedAt.IsZero() {
		finishedAt = time.Now().UTC()
	}
	partitionTime := finishedAt
	if partitionTime.IsZero() {
		partitionTime = time.Now().UTC()
	}
	if err := p.ensurePartition("call_traces", partitionTime); err != nil {
		return err
	}
	original, err := json.Marshal(trace.OriginalRequestHeaders)
	if err != nil {
		return err
	}
	outbound, err := json.Marshal(trace.OutboundRequestHeaders)
	if err != nil {
		return err
	}
	response, err := json.Marshal(trace.ResponseHeaders)
	if err != nil {
		return err
	}
	tx, err := p.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if trace.ID > 0 {
		if _, err := tx.Exec(`DELETE FROM call_traces WHERE id=$1`, trace.ID); err != nil {
			return err
		}
	}
	args := []any{trace.UserID, trace.APIKey, trace.AIProviderType, trace.AccountID, trace.RequestID, trace.SessionID, trace.SourceIP, trace.URL, trace.RequestMethod, trace.OutboundURL, trace.HTTPErrorCode, trace.HTTPErrorInfo, string(original), string(outbound), trace.RequestBody, string(response), trace.ResponseBody, trace.RequestBytes, trace.ResponseBytes, trace.QueueDurationMs, trace.RequestDurationMs, trace.Model, trace.InputTokens, trace.OutputTokens, trace.CacheCreationTokens, trace.CacheReadTokens, databaseTime(finishedAt)}
	if trace.ID > 0 {
		args = append([]any{trace.ID}, args...)
	}
	var query string
	// Keep the two insert forms separate: an identity column must be omitted
	// for new rows, while an explicit ID is used for SQLite-compatible updates.
	if trace.ID > 0 {
		query = `INSERT INTO call_traces(id, user_id, apikey, provider_type, account_id, request_id, session_id, source_ip, url, request_method, outbound_url, http_error_code, http_error_info, original_request_headers, outbound_request_headers, request_body, response_headers, response_body, request_bytes, response_bytes, queue_duration_ms, request_duration_ms, model, input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, finished_at)
			VALUES($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14::jsonb, $15::jsonb, $16, $17::jsonb, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28) RETURNING id`
	} else {
		query = `INSERT INTO call_traces(user_id, apikey, provider_type, account_id, request_id, session_id, source_ip, url, request_method, outbound_url, http_error_code, http_error_info, original_request_headers, outbound_request_headers, request_body, response_headers, response_body, request_bytes, response_bytes, queue_duration_ms, request_duration_ms, model, input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, finished_at)
			VALUES($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13::jsonb, $14::jsonb, $15, $16::jsonb, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27) RETURNING id`
	}
	if err := tx.QueryRow(query, args...).Scan(&trace.ID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (p *PostgreSQLDatabase) GetCallTrace(finishedAt time.Time, id int) (*PersistedCallTrace, error) {
	if err := p.ensureOpen(); err != nil {
		return nil, err
	}
	query := `SELECT id, user_id, apikey, provider_type, account_id, request_id, session_id, source_ip, url, request_method, outbound_url, http_error_code, http_error_info, original_request_headers, outbound_request_headers, request_body, response_headers, response_body, request_bytes, response_bytes, queue_duration_ms, request_duration_ms, model, input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, finished_at FROM call_traces WHERE id=$1`
	args := []any{id}
	if !finishedAt.IsZero() {
		dayStart := utcDay(finishedAt)
		query += ` AND finished_at >= $2 AND finished_at < $3`
		args = append(args, dayStart, dayStart.AddDate(0, 0, 1))
	}
	var trace PersistedCallTrace
	var original, outbound, response []byte
	err := p.db.QueryRow(query, args...).Scan(&trace.ID, &trace.UserID, &trace.APIKey, &trace.AIProviderType, &trace.AccountID, &trace.RequestID, &trace.SessionID, &trace.SourceIP, &trace.URL, &trace.RequestMethod, &trace.OutboundURL, &trace.HTTPErrorCode, &trace.HTTPErrorInfo, &original, &outbound, &trace.RequestBody, &response, &trace.ResponseBody, &trace.RequestBytes, &trace.ResponseBytes, &trace.QueueDurationMs, &trace.RequestDurationMs, &trace.Model, &trace.InputTokens, &trace.OutputTokens, &trace.CacheCreationTokens, &trace.CacheReadTokens, &trace.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCallTraceNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := unmarshalJSONColumn(original, &trace.OriginalRequestHeaders); err != nil {
		return nil, err
	}
	if err := unmarshalJSONColumn(outbound, &trace.OutboundRequestHeaders); err != nil {
		return nil, err
	}
	if err := unmarshalJSONColumn(response, &trace.ResponseHeaders); err != nil {
		return nil, err
	}
	return &trace, nil
}

func (p *PostgreSQLDatabase) CleanupCallTrace(days int) error {
	if err := p.ensureOpen(); err != nil {
		return err
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format("20060102")
	partitions, err := p.listPartitions("call_traces")
	if err != nil {
		return err
	}
	for _, partition := range partitions {
		if tracePartitionPattern.MatchString(partition) && strings.TrimPrefix(partition, "call_traces_") < cutoff {
			if _, err := p.db.Exec("DROP TABLE " + quoteIdentifier(partition)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *PostgreSQLDatabase) QueryCallTraces(filter CallTraceFilter, page, pageSize int) ([]PersistedCallTraceSummary, int, error) {
	if err := p.ensureOpen(); err != nil {
		return nil, 0, err
	}
	where, args := p.callTraceWhere(filter)
	var total int
	if err := p.db.QueryRow(`SELECT COUNT(*) FROM call_traces t `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := p.db.Query(`SELECT t.id, t.user_id, t.apikey, t.provider_type, t.account_id, t.request_id, t.session_id, t.source_ip,
		t.url, t.request_method, t.outbound_url, t.http_error_code, t.http_error_info, t.model, t.input_tokens, t.output_tokens,
		t.cache_creation_tokens, t.cache_read_tokens, t.queue_duration_ms, t.request_duration_ms, t.finished_at,
		COALESCE((SELECT u.name FROM api_keys k JOIN users u ON u.id=k.user_id
			WHERE k.key_value=t.apikey OR CAST(k.id AS TEXT)=t.apikey LIMIT 1), '')
		FROM call_traces t `+where+` ORDER BY t.finished_at DESC NULLS LAST, t.id DESC LIMIT $`+fmt.Sprint(len(args)-1)+` OFFSET $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	result := make([]PersistedCallTraceSummary, 0, pageSize)
	for rows.Next() {
		var trace PersistedCallTraceSummary
		if err := rows.Scan(&trace.ID, &trace.UserID, &trace.APIKey, &trace.AIProviderType, &trace.AccountID, &trace.RequestID, &trace.SessionID, &trace.SourceIP, &trace.URL, &trace.RequestMethod, &trace.OutboundURL, &trace.HTTPErrorCode, &trace.HTTPErrorInfo, &trace.Model, &trace.InputTokens, &trace.OutputTokens, &trace.CacheCreationTokens, &trace.CacheReadTokens, &trace.QueueDurationMs, &trace.RequestDurationMs, &trace.FinishedAt, &trace.Username); err != nil {
			return nil, 0, err
		}
		result = append(result, trace)
	}
	return result, total, rows.Err()
}

func (p *PostgreSQLDatabase) QueryAccountUsage(timeRange TimeRange) ([]AccountUsageRow, UsageTotals, error) {
	if err := p.ensureOpen(); err != nil {
		return nil, UsageTotals{}, err
	}
	rows, err := p.db.Query(`SELECT account_id, COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		COALESCE(SUM(cache_creation_tokens),0), COALESCE(SUM(cache_read_tokens),0)
		FROM call_traces WHERE finished_at >= $1 AND finished_at <= $2 GROUP BY account_id ORDER BY account_id`, timeRange.Start.UTC(), timeRange.End.UTC())
	if err != nil {
		return nil, UsageTotals{}, err
	}
	defer rows.Close()
	result := make([]AccountUsageRow, 0)
	var totals UsageTotals
	for rows.Next() {
		var row AccountUsageRow
		if err := rows.Scan(&row.AccountID, &row.Usage.Requests, &row.Usage.InputTokens, &row.Usage.OutputTokens, &row.Usage.CacheCreationTokens, &row.Usage.CacheReadTokens); err != nil {
			return nil, UsageTotals{}, err
		}
		row.Usage.Finalize()
		totals.add(row.Usage)
		result = append(result, row)
	}
	return result, totals, rows.Err()
}

func (p *PostgreSQLDatabase) QueryUserUsage(timeRange TimeRange, accountID int) ([]UserUsageRow, UsageTotals, error) {
	if err := p.ensureOpen(); err != nil {
		return nil, UsageTotals{}, err
	}
	query := `SELECT COALESCE(k.user_id,0), COUNT(*), COALESCE(SUM(t.input_tokens),0), COALESCE(SUM(t.output_tokens),0),
		COALESCE(SUM(t.cache_creation_tokens),0), COALESCE(SUM(t.cache_read_tokens),0)
		FROM call_traces t LEFT JOIN api_keys k ON k.key_value=t.apikey OR CAST(k.id AS TEXT)=t.apikey
		WHERE t.finished_at >= $1 AND t.finished_at <= $2`
	args := []any{timeRange.Start.UTC(), timeRange.End.UTC()}
	if accountID > 0 {
		query += ` AND t.account_id=$3`
		args = append(args, accountID)
	}
	query += ` GROUP BY COALESCE(k.user_id,0) ORDER BY 1`
	rows, err := p.db.Query(query, args...)
	if err != nil {
		return nil, UsageTotals{}, err
	}
	defer rows.Close()
	result := make([]UserUsageRow, 0)
	var totals UsageTotals
	for rows.Next() {
		var row UserUsageRow
		if err := rows.Scan(&row.UserID, &row.Usage.Requests, &row.Usage.InputTokens, &row.Usage.OutputTokens, &row.Usage.CacheCreationTokens, &row.Usage.CacheReadTokens); err != nil {
			return nil, UsageTotals{}, err
		}
		row.Usage.Finalize()
		totals.add(row.Usage)
		result = append(result, row)
	}
	return result, totals, rows.Err()
}

func (p *PostgreSQLDatabase) callTraceWhere(filter CallTraceFilter) (string, []any) {
	conditions := []string{"t.finished_at >= $1", "t.finished_at <= $2"}
	args := []any{filter.TimeRange.Start.UTC(), filter.TimeRange.End.UTC()}
	add := func(condition string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(condition, len(args)))
	}
	if filter.AccountID != 0 {
		add("t.account_id = $%d", filter.AccountID)
	}
	if filter.Code != nil {
		add("t.http_error_code = $%d", *filter.Code)
	}
	if filter.UserID != nil {
		if *filter.UserID == 0 {
			conditions = append(conditions, "NOT EXISTS (SELECT 1 FROM api_keys k WHERE k.key_value=t.apikey OR CAST(k.id AS TEXT)=t.apikey)")
		} else {
			add("EXISTS (SELECT 1 FROM api_keys k WHERE (k.key_value=t.apikey OR CAST(k.id AS TEXT)=t.apikey) AND k.user_id=$%d)", *filter.UserID)
		}
	}
	if filter.Search != "" {
		value := filter.Search
		args = append(args, value)
		placeholder := fmt.Sprintf("$%d", len(args))
		condition := "(t.source_ip=" + placeholder + " OR t.model=" + placeholder + " OR t.request_id=" + placeholder + " OR t.session_id=" + placeholder
		if filter.SearchUsernames {
			condition += " OR EXISTS (SELECT 1 FROM api_keys k JOIN users u ON u.id=k.user_id WHERE (k.key_value=t.apikey OR CAST(k.id AS TEXT)=t.apikey) AND u.name=" + placeholder + ")"
		}
		conditions = append(conditions, condition+")")
	}
	return "WHERE " + strings.Join(conditions, " AND "), args
}

func (p *PostgreSQLDatabase) ensureOpen() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.db == nil {
		return errors.New("postgresql database is not open")
	}
	return nil
}

func (p *PostgreSQLDatabase) ensurePartition(parent string, value time.Time) error {
	if value.IsZero() {
		return nil
	}
	if parent != "call_traces" && parent != "proxy_logs" {
		return errors.New("invalid partitioned table")
	}
	dayStart := utcDay(value)
	name := parent + "_" + dayStart.Format("20060102")
	start := dayStart.Format("2006-01-02 15:04:05Z07:00")
	end := dayStart.AddDate(0, 0, 1).Format("2006-01-02 15:04:05Z07:00")
	p.partitionMu.Lock()
	defer p.partitionMu.Unlock()
	_, err := p.db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s PARTITION OF %s FOR VALUES FROM ('%s') TO ('%s')`, quoteIdentifier(name), quoteIdentifier(parent), start, end))
	return err
}

func (p *PostgreSQLDatabase) listPartitions(parent string) ([]string, error) {
	rows, err := p.db.Query(`SELECT child.relname FROM pg_inherits
		JOIN pg_class child ON child.oid=pg_inherits.inhrelid
		JOIN pg_class parent_table ON parent_table.oid=pg_inherits.inhparent
		WHERE parent_table.relname=$1`, parent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		result = append(result, name)
	}
	return result, rows.Err()
}

func utcDay(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func databaseTime(value time.Time) time.Time {
	return value.UTC()
}

func unmarshalJSONColumn(raw []byte, target any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, target)
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

var (
	tracePartitionPattern = regexp.MustCompile(`^call_traces_[0-9]{8}$`)
	proxyPartitionPattern = regexp.MustCompile(`^proxy_logs_[0-9]{8}$`)
)

var postgresSchema = []string{
	`CREATE TABLE IF NOT EXISTS proxy_stats (address TEXT NOT NULL, application TEXT NOT NULL, start_at BIGINT NOT NULL, source TEXT NOT NULL, requests BIGINT NOT NULL, failures BIGINT NOT NULL, PRIMARY KEY(address, application, start_at, source))`,
	`CREATE TABLE IF NOT EXISTS oauth_credentials (id TEXT PRIMARY KEY, credential JSONB NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS accounts (id BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY, provider TEXT NOT NULL, name TEXT NOT NULL, config JSONB NOT NULL, state JSONB NOT NULL DEFAULT '{}'::jsonb, quota JSONB NOT NULL DEFAULT '{}'::jsonb, created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS idx_accounts_name ON accounts(name)`,
	`CREATE INDEX IF NOT EXISTS idx_accounts_provider ON accounts(provider)`,
	`CREATE TABLE IF NOT EXISTS users (id BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY, name TEXT NOT NULL, labels JSONB NOT NULL, role TEXT NOT NULL, enabled BOOLEAN NOT NULL DEFAULT TRUE, password_hash TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS idx_users_name ON users(name)`,
	`CREATE TABLE IF NOT EXISTS api_keys (id BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY, user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE, account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE, name TEXT NOT NULL DEFAULT '', key_value TEXT NOT NULL, config JSONB NOT NULL DEFAULT '{}'::jsonb, valid_seconds BIGINT NOT NULL DEFAULT 0, created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS idx_api_keys_user_id ON api_keys(user_id)`,
	`CREATE INDEX IF NOT EXISTS idx_api_keys_account_id ON api_keys(account_id)`,
	`CREATE INDEX IF NOT EXISTS idx_api_keys_key_value ON api_keys(key_value)`,
	`CREATE TABLE IF NOT EXISTS proxy_groups (id BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY, config JSONB NOT NULL DEFAULT '{}'::jsonb, state JSONB NOT NULL DEFAULT '{}'::jsonb, created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS configs (id BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY, type TEXT NOT NULL, name TEXT NOT NULL, value JSONB NOT NULL, UNIQUE(type, name))`,
	`CREATE INDEX IF NOT EXISTS idx_configs_type ON configs(type)`,
	`CREATE TABLE IF NOT EXISTS call_traces (id BIGINT GENERATED BY DEFAULT AS IDENTITY, user_id BIGINT NOT NULL DEFAULT 0, apikey TEXT, provider_type TEXT, account_id BIGINT, request_id TEXT, session_id TEXT NOT NULL DEFAULT '', source_ip TEXT, url TEXT, request_method TEXT NOT NULL DEFAULT '', outbound_url TEXT NOT NULL DEFAULT '', http_error_code INTEGER, http_error_info TEXT, original_request_headers JSONB, outbound_request_headers JSONB, request_body BYTEA, response_headers JSONB, response_body BYTEA, request_bytes BIGINT NOT NULL DEFAULT 0, response_bytes BIGINT NOT NULL DEFAULT 0, queue_duration_ms BIGINT NOT NULL DEFAULT 0, request_duration_ms BIGINT NOT NULL DEFAULT 0, model TEXT, input_tokens INTEGER, output_tokens INTEGER, cache_creation_tokens INTEGER, cache_read_tokens INTEGER, finished_at TIMESTAMPTZ) PARTITION BY RANGE (finished_at)`,
	`CREATE TABLE IF NOT EXISTS call_traces_default PARTITION OF call_traces DEFAULT`,
	`CREATE INDEX IF NOT EXISTS idx_call_traces_finished_at ON call_traces(finished_at)`,
	`CREATE INDEX IF NOT EXISTS idx_call_traces_id ON call_traces(id)`,
	`CREATE INDEX IF NOT EXISTS idx_call_traces_account_id ON call_traces(account_id)`,
	`CREATE INDEX IF NOT EXISTS idx_call_traces_apikey ON call_traces(apikey)`,
	`CREATE INDEX IF NOT EXISTS idx_call_traces_request_id ON call_traces(request_id)`,
	`CREATE TABLE IF NOT EXISTS proxy_logs (group_id BIGINT NOT NULL, proxy_url TEXT NOT NULL, url TEXT NOT NULL DEFAULT '', app_type TEXT NOT NULL DEFAULT '', http_error_code INTEGER NOT NULL, http_error_message TEXT NOT NULL, time TIMESTAMPTZ NOT NULL) PARTITION BY RANGE (time)`,
	`CREATE TABLE IF NOT EXISTS proxy_logs_default PARTITION OF proxy_logs DEFAULT`,
	`CREATE INDEX IF NOT EXISTS idx_proxy_logs_time ON proxy_logs(time)`,
	`CREATE INDEX IF NOT EXISTS idx_proxy_logs_group_id ON proxy_logs(group_id)`,
}

func migrateLegacyModuleConfigsPostgreSQL(db *sql.DB) error {
	var exists bool
	if err := db.QueryRow(`SELECT to_regclass('module_configs') IS NOT NULL`).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return nil
	}
	_, err := db.Exec(`INSERT INTO configs(type, name, value)
		SELECT $1, module, config FROM module_configs
		ON CONFLICT (type, name) DO NOTHING`, ModuleConfigType)
	return err
}

var _ Database = (*PostgreSQLDatabase)(nil)
