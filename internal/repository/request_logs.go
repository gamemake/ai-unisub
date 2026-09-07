package repository

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/database"
	"github.com/ai-unisub/ai-unisub/internal/model"
)

var requestLogTablePattern = regexp.MustCompile(`^request_logs_([0-9]{8})$`)

type RequestLog struct {
	ID                  int64     `json:"id"`
	Day                 string    `json:"day"`
	SubscriptionID      *int64    `json:"subscription_id,omitempty"`
	SubscriptionName    string    `json:"subscription_name,omitempty"`
	APIKeyID            *int64    `json:"api_key_id,omitempty"`
	UserID              *int64    `json:"user_id,omitempty"`
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
	SubscriptionID *int64
	APIKeyID       *int64
	Provider       string
	StatusCode     *int
	Query          string
	Limit          int
	Offset         int
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
	if err := createRequestLogTable(ctx, tx, r.db.Dialect, table); err != nil {
		return err
	}
	duration := entry.FinishedAt.Sub(entry.StartedAt)
	if duration < 0 {
		duration = 0
	}
	query := fmt.Sprintf(`INSERT INTO %s
		(subscription_id, api_key_id, user_id, provider, method, path, query, client_ip, status_code, started_at, finished_at, duration_ms,
		 request_id, error_type, model, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, total_tokens,
		 request_headers, request_body, response_headers, response_body, request_truncated, response_truncated)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, table)
	_, err = tx.ExecContext(ctx, query,
		nullableInt64(entry.SubscriptionID), nullableInt64(entry.APIKeyID), nullableInt64(entry.UserID), nullableString(entry.Provider),
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
	if entry.SubscriptionID != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE subscriptions SET last_used_at=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
			entry.FinishedAt.UTC().Format(time.RFC3339Nano), *entry.SubscriptionID); err != nil {
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
		if err := ensureRequestLogColumns(ctx, r.db, r.db.Dialect, table); err != nil {
			return nil, 0, err
		}
	}
	unions := make([]string, 0, len(tables))
	for _, table := range tables {
		day := strings.TrimPrefix(table, "request_logs_")
		unions = append(unions, fmt.Sprintf(`SELECT '%s' AS day, l.id, l.subscription_id, a.name AS subscription_name, l.api_key_id, k.name AS api_key_name, k.key_prefix,
			l.provider, l.method, l.path, l.query, l.client_ip, l.status_code, l.started_at, l.finished_at, l.duration_ms, l.request_id, l.error_type,
			l.model, l.input_tokens, l.output_tokens, l.cache_read_tokens, l.cache_creation_tokens, l.total_tokens,
			l.request_truncated, l.response_truncated
			FROM %s l
			LEFT JOIN subscriptions a ON a.id=l.subscription_id
			LEFT JOIN api_keys k ON k.id=l.api_key_id`, day, table))
	}
	from := "(" + strings.Join(unions, " UNION ALL ") + ")"
	where, args := requestLogWhere(r.db.Dialect, filter)
	var total int
	countQuery := "SELECT COUNT(*) FROM " + from + " logs " + where
	if err := r.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	listQuery := "SELECT day, id, subscription_id, subscription_name, api_key_id, api_key_name, key_prefix, provider, method, path, query, client_ip, status_code, started_at, finished_at, duration_ms, request_id, error_type, model, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, total_tokens, request_truncated, response_truncated FROM " +
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
	exists, err := r.db.TableExists(ctx, table)
	if err != nil {
		return RequestLog{}, err
	}
	if !exists {
		return RequestLog{}, ErrNotFound
	}
	if err := ensureRequestLogColumns(ctx, r.db, r.db.Dialect, table); err != nil {
		return RequestLog{}, err
	}
	query := fmt.Sprintf(`SELECT '%s' AS day, l.id, l.subscription_id, a.name AS subscription_name, l.api_key_id, k.name AS api_key_name, k.key_prefix,
		l.provider, l.method, l.path, l.query, l.client_ip, l.status_code, l.started_at, l.finished_at, l.duration_ms, l.request_id, l.error_type,
		l.model, l.input_tokens, l.output_tokens, l.cache_read_tokens, l.cache_creation_tokens, l.total_tokens,
		l.request_headers, l.request_body, l.response_headers, l.response_body, l.request_truncated, l.response_truncated
		FROM %s l
		LEFT JOIN subscriptions a ON a.id=l.subscription_id
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
	names, err := r.db.ListTablesLike(ctx, "request_logs_%")
	if err != nil {
		return nil, err
	}
	var expired []string
	for _, table := range names {
		match := requestLogTablePattern.FindStringSubmatch(table)
		if match == nil {
			continue
		}
		date, err := time.ParseInLocation("20060102", match[1], location)
		if err == nil && date.Before(oldestRetainedDate) {
			expired = append(expired, table)
		}
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
	names, err := r.db.ListTablesLike(ctx, "request_logs_%")
	if err != nil {
		return nil, err
	}
	var tables []string
	for _, table := range names {
		if requestLogTablePattern.MatchString(table) {
			tables = append(tables, table)
		}
	}
	sort.Slice(tables, func(i, j int) bool { return tables[i] > tables[j] })
	return tables, nil
}

func requestLogTablesInRange(tables []string, since, until time.Time, location *time.Location) []string {
	if location == nil {
		location = time.Local
	}
	sinceDay := since.In(location).Format("20060102")
	untilDay := until.In(location).Format("20060102")
	out := make([]string, 0, len(tables))
	for _, table := range tables {
		day := strings.TrimPrefix(table, "request_logs_")
		if day >= sinceDay && day <= untilDay {
			out = append(out, table)
		}
	}
	return out
}

func (r *Repository) usageTotalsFromRequestLogs(ctx context.Context, subscriptionID int64, since, until time.Time, location *time.Location) (model.UsageTotals, error) {
	var totals model.UsageTotals
	if location == nil {
		location = time.Local
	}
	tables, err := r.requestLogTables(ctx)
	if err != nil {
		return totals, err
	}
	tables = requestLogTablesInRange(tables, since, until, location)
	if len(tables) == 0 {
		return totals, nil
	}
	for _, table := range tables {
		if err := ensureRequestLogColumns(ctx, r.db, r.db.Dialect, table); err != nil {
			return totals, err
		}
	}
	unions := make([]string, 0, len(tables))
	args := make([]any, 0, len(tables)*3)
	sinceText := since.UTC().Format(time.RFC3339Nano)
	untilText := until.UTC().Format(time.RFC3339Nano)
	for _, table := range tables {
		unions = append(unions, fmt.Sprintf(`SELECT input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, total_tokens
			FROM %s WHERE subscription_id=? AND started_at>=? AND started_at<?`, table))
		args = append(args, subscriptionID, sinceText, untilText)
	}
	query := `SELECT COUNT(*),
		COALESCE(SUM(input_tokens),0),
		COALESCE(SUM(output_tokens),0),
		COALESCE(SUM(cache_read_tokens),0),
		COALESCE(SUM(cache_creation_tokens),0),
		COALESCE(SUM(CASE WHEN total_tokens IS NOT NULL THEN total_tokens ELSE COALESCE(input_tokens,0)+COALESCE(output_tokens,0) END),0)
		FROM (` + strings.Join(unions, " UNION ALL ") + `)`
	err = r.db.QueryRowContext(ctx, query, args...).Scan(
		&totals.Requests,
		&totals.InputTokens,
		&totals.OutputTokens,
		&totals.CacheReadTokens,
		&totals.CacheCreationTokens,
		&totals.TotalTokens,
	)
	return totals, err
}

func (r *Repository) UsageBySubscription(ctx context.Context, since, until time.Time, location *time.Location) ([]model.SubscriptionUsageRow, model.UsageTotals, bool, error) {
	var grand model.UsageTotals
	if location == nil {
		location = time.Local
	}
	if !until.After(since) {
		return []model.SubscriptionUsageRow{}, grand, false, fmt.Errorf("until must be after since")
	}
	tables, err := r.requestLogTables(ctx)
	if err != nil {
		return nil, grand, false, err
	}
	tables = requestLogTablesInRange(tables, since, until, location)
	subscriptions, err := r.ListSubscriptions(ctx)
	if err != nil {
		return nil, grand, false, err
	}
	byID := map[int64]model.SubscriptionUsageRow{}
	for _, subscription := range subscriptions {
		byID[subscription.ID] = model.SubscriptionUsageRow{
			SubscriptionID:   subscription.ID,
			SubscriptionName: subscription.Name,
			Provider:         subscription.Provider,
		}
	}
	if len(tables) == 0 {
		rows := make([]model.SubscriptionUsageRow, 0, len(subscriptions))
		for _, subscription := range subscriptions {
			rows = append(rows, byID[subscription.ID])
		}
		return rows, grand, false, nil
	}
	for _, table := range tables {
		if err := ensureRequestLogColumns(ctx, r.db, r.db.Dialect, table); err != nil {
			return nil, grand, false, err
		}
	}
	unions := make([]string, 0, len(tables))
	args := make([]any, 0, len(tables)*2)
	sinceText := since.UTC().Format(time.RFC3339Nano)
	untilText := until.UTC().Format(time.RFC3339Nano)
	for _, table := range tables {
		unions = append(unions, fmt.Sprintf(`SELECT subscription_id,
			COUNT(*) AS requests,
			COALESCE(SUM(input_tokens),0) AS input_tokens,
			COALESCE(SUM(output_tokens),0) AS output_tokens,
			COALESCE(SUM(cache_read_tokens),0) AS cache_read_tokens,
			COALESCE(SUM(cache_creation_tokens),0) AS cache_creation_tokens,
			COALESCE(SUM(CASE WHEN total_tokens IS NOT NULL THEN total_tokens ELSE COALESCE(input_tokens,0)+COALESCE(output_tokens,0) END),0) AS total_tokens
			FROM %s WHERE started_at>=? AND started_at<? AND subscription_id IS NOT NULL
			GROUP BY subscription_id`, table))
		args = append(args, sinceText, untilText)
	}
	query := `SELECT subscription_id,
		COALESCE(SUM(requests),0),
		COALESCE(SUM(input_tokens),0),
		COALESCE(SUM(output_tokens),0),
		COALESCE(SUM(cache_read_tokens),0),
		COALESCE(SUM(cache_creation_tokens),0),
		COALESCE(SUM(total_tokens),0)
		FROM (` + strings.Join(unions, " UNION ALL ") + `) usage_parts
		GROUP BY subscription_id
		ORDER BY subscription_id`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, grand, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var usage model.UsageTotals
		if err := rows.Scan(&id, &usage.Requests, &usage.InputTokens, &usage.OutputTokens, &usage.CacheReadTokens, &usage.CacheCreationTokens, &usage.TotalTokens); err != nil {
			return nil, grand, false, err
		}
		row := byID[id]
		if row.SubscriptionID == 0 {
			row.SubscriptionID = id
			row.SubscriptionName = fmt.Sprintf("subscription %d", id)
		}
		row.Usage = usage
		byID[id] = row
		grand.Requests += usage.Requests
		grand.InputTokens += usage.InputTokens
		grand.OutputTokens += usage.OutputTokens
		grand.CacheReadTokens += usage.CacheReadTokens
		grand.CacheCreationTokens += usage.CacheCreationTokens
		grand.TotalTokens += usage.TotalTokens
	}
	if err := rows.Err(); err != nil {
		return nil, grand, false, err
	}
	seen := map[int64]bool{}
	out := make([]model.SubscriptionUsageRow, 0, len(byID))
	for _, subscription := range subscriptions {
		out = append(out, byID[subscription.ID])
		seen[subscription.ID] = true
	}
	orphans := make([]model.SubscriptionUsageRow, 0)
	for id, row := range byID {
		if !seen[id] {
			orphans = append(orphans, row)
		}
	}
	sort.Slice(orphans, func(i, j int) bool { return orphans[i].SubscriptionID < orphans[j].SubscriptionID })
	out = append(out, orphans...)
	return out, grand, false, nil
}

func (r *Repository) UsageByUser(ctx context.Context, since, until time.Time, location *time.Location, subscriptionID *int64) ([]model.UserUsageRow, model.UsageTotals, bool, error) {
	var grand model.UsageTotals
	if location == nil {
		location = time.Local
	}
	if !until.After(since) {
		return []model.UserUsageRow{}, grand, false, fmt.Errorf("until must be after since")
	}
	tables, err := r.requestLogTables(ctx)
	if err != nil {
		return nil, grand, false, err
	}
	tables = requestLogTablesInRange(tables, since, until, location)
	users, err := r.ListUsers(ctx)
	if err != nil {
		return nil, grand, false, err
	}
	byID := map[int64]model.UserUsageRow{}
	for _, user := range users {
		id := user.ID
		byID[id] = model.UserUsageRow{UserID: &id, Username: user.Username, Role: user.Role, Enabled: user.Enabled}
	}
	if len(tables) == 0 {
		return []model.UserUsageRow{}, grand, false, nil
	}
	for _, table := range tables {
		if err := ensureRequestLogColumns(ctx, r.db, r.db.Dialect, table); err != nil {
			return nil, grand, false, err
		}
	}
	unions := make([]string, 0, len(tables))
	args := make([]any, 0, len(tables)*3)
	sinceText := since.UTC().Format(time.RFC3339Nano)
	untilText := until.UTC().Format(time.RFC3339Nano)
	for _, table := range tables {
		subscriptionFilter := ""
		if subscriptionID != nil {
			subscriptionFilter = " AND l.subscription_id=?"
		}
		unions = append(unions, fmt.Sprintf(`SELECT COALESCE(l.user_id,k.user_id) AS user_id,
			COUNT(*) AS requests,
			COALESCE(SUM(l.input_tokens),0) AS input_tokens,
			COALESCE(SUM(l.output_tokens),0) AS output_tokens,
			COALESCE(SUM(l.cache_read_tokens),0) AS cache_read_tokens,
			COALESCE(SUM(l.cache_creation_tokens),0) AS cache_creation_tokens,
			COALESCE(SUM(CASE WHEN l.total_tokens IS NOT NULL THEN l.total_tokens ELSE COALESCE(l.input_tokens,0)+COALESCE(l.output_tokens,0) END),0) AS total_tokens
			FROM %s l LEFT JOIN api_keys k ON k.id=l.api_key_id
			WHERE l.started_at>=? AND l.started_at<? AND l.subscription_id IS NOT NULL%s
			GROUP BY COALESCE(l.user_id,k.user_id)`, table, subscriptionFilter))
		args = append(args, sinceText, untilText)
		if subscriptionID != nil {
			args = append(args, *subscriptionID)
		}
	}
	query := `SELECT user_id,
		COALESCE(SUM(requests),0),
		COALESCE(SUM(input_tokens),0),
		COALESCE(SUM(output_tokens),0),
		COALESCE(SUM(cache_read_tokens),0),
		COALESCE(SUM(cache_creation_tokens),0),
		COALESCE(SUM(total_tokens),0)
		FROM (` + strings.Join(unions, " UNION ALL ") + `) usage_parts
		GROUP BY user_id
		ORDER BY user_id`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, grand, false, err
	}
	defer rows.Close()
	var unassigned *model.UserUsageRow
	for rows.Next() {
		var id sql.NullInt64
		var usage model.UsageTotals
		if err := rows.Scan(&id, &usage.Requests, &usage.InputTokens, &usage.OutputTokens, &usage.CacheReadTokens, &usage.CacheCreationTokens, &usage.TotalTokens); err != nil {
			return nil, grand, false, err
		}
		if id.Valid {
			row := byID[id.Int64]
			if row.UserID == nil {
				value := id.Int64
				row.UserID = &value
				row.Username = fmt.Sprintf("用户 %d", value)
			}
			row.Usage = usage
			byID[id.Int64] = row
		} else {
			row := model.UserUsageRow{Username: "未归属", Usage: usage}
			unassigned = &row
		}
		grand.Requests += usage.Requests
		grand.InputTokens += usage.InputTokens
		grand.OutputTokens += usage.OutputTokens
		grand.CacheReadTokens += usage.CacheReadTokens
		grand.CacheCreationTokens += usage.CacheCreationTokens
		grand.TotalTokens += usage.TotalTokens
	}
	if err := rows.Err(); err != nil {
		return nil, grand, false, err
	}
	seen := map[int64]bool{}
	out := make([]model.UserUsageRow, 0, len(byID)+1)
	for _, user := range users {
		row := byID[user.ID]
		if row.Usage.Requests != 0 || row.Usage.InputTokens != 0 || row.Usage.OutputTokens != 0 || row.Usage.CacheReadTokens != 0 || row.Usage.CacheCreationTokens != 0 || row.Usage.TotalTokens != 0 {
			out = append(out, row)
		}
		seen[user.ID] = true
	}
	orphanIDs := make([]int64, 0)
	for id := range byID {
		if !seen[id] {
			orphanIDs = append(orphanIDs, id)
		}
	}
	sort.Slice(orphanIDs, func(i, j int) bool { return orphanIDs[i] < orphanIDs[j] })
	for _, id := range orphanIDs {
		out = append(out, byID[id])
	}
	if unassigned != nil {
		out = append(out, *unassigned)
	}
	return out, grand, false, nil
}

func ResolveUsageRange(now time.Time, location *time.Location, rangeName, from, to string) (time.Time, time.Time, error) {
	if location == nil {
		location = time.Local
	}
	now = now.In(location)
	rangeName = strings.TrimSpace(strings.ToLower(rangeName))
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if from != "" || to != "" {
		if from == "" || to == "" {
			return time.Time{}, time.Time{}, fmt.Errorf("from and to are both required")
		}
		startDay, err := time.ParseInLocation("2006-01-02", from, location)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid from date")
		}
		endDay, err := time.ParseInLocation("2006-01-02", to, location)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid to date")
		}
		until := endDay.AddDate(0, 0, 1)
		if !until.After(startDay) {
			return time.Time{}, time.Time{}, fmt.Errorf("to must be on or after from")
		}
		return startDay, until, nil
	}
	switch rangeName {
	case "", "1d":
		return now.Add(-24 * time.Hour), now, nil
	case "1w":
		return now.AddDate(0, 0, -7), now, nil
	case "1m":
		return now.AddDate(0, 0, -30), now, nil
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("range must be 1d, 1w, or 1m")
	}
}

func requestLogWhere(dialect database.Dialect, filter RequestLogFilter) (string, []any) {
	clauses := []string{"1=1"}
	var args []any
	if filter.SubscriptionID != nil {
		clauses = append(clauses, "subscription_id=?")
		args = append(args, *filter.SubscriptionID)
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
		op := dialect.LikeOperator()
		clauses = append(clauses, fmt.Sprintf(`(COALESCE(path,'') %s ? OR COALESCE(query,'') %s ? OR COALESCE(client_ip,'') %s ? OR COALESCE(model,'') %s ? OR COALESCE(request_id,'') %s ? OR COALESCE(error_type,'') %s ? OR COALESCE(subscription_name,'') %s ? OR COALESCE(api_key_name,'') %s ?)`, op, op, op, op, op, op, op, op))
		args = append(args, like, like, like, like, like, like, like, like)
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

func requestLogTableName(at time.Time, location *time.Location) string {
	return "request_logs_" + at.In(location).Format("20060102")
}

func createRequestLogTable(ctx context.Context, tx *database.Tx, dialect database.Dialect, table string) error {
	if !requestLogTablePattern.MatchString(table) {
		return fmt.Errorf("invalid request log table name %q", table)
	}
	statement, err := database.RequestLogTableSQL(dialect, table)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, statement); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%s_subscription_started ON %s(subscription_id, started_at DESC)`, table, table)); err != nil {
		return err
	}
	return ensureRequestLogColumns(ctx, tx, dialect, table)
}

type schemaDB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	ColumnNames(ctx context.Context, table string) (map[string]bool, error)
}

func ensureRequestLogColumns(ctx context.Context, db schemaDB, dialect database.Dialect, table string) error {
	if !requestLogTablePattern.MatchString(table) {
		return fmt.Errorf("invalid request log table name %q", table)
	}
	existing, err := db.ColumnNames(ctx, table)
	if err != nil {
		return err
	}
	for _, column := range database.RequestLogColumnDefs(dialect) {
		if existing[column.Name] {
			continue
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column.Name, column.Definition)); err != nil {
			return fmt.Errorf("add %s.%s: %w", table, column.Name, err)
		}
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%s_user_started ON %s(user_id, started_at DESC)`, table, table)); err != nil {
		return fmt.Errorf("create %s user usage index: %w", table, err)
	}
	return nil
}

type requestLogScanner interface {
	Scan(dest ...any) error
}

func scanRequestLogSummary(s requestLogScanner) (RequestLog, error) {
	var entry RequestLog
	var subscriptionID, apiKeyID, input, output, cacheRead, cacheCreation, total sql.NullInt64
	var subscriptionName, apiKeyName, prefix, provider, query, clientIP, requestID, errorType, model sql.NullString
	var started, finished string
	var requestTrunc, responseTrunc int
	if err := s.Scan(&entry.Day, &entry.ID, &subscriptionID, &subscriptionName, &apiKeyID, &apiKeyName, &prefix, &provider,
		&entry.Method, &entry.Path, &query, &clientIP, &entry.StatusCode, &started, &finished, &entry.DurationMs, &requestID, &errorType,
		&model, &input, &output, &cacheRead, &cacheCreation, &total, &requestTrunc, &responseTrunc); err != nil {
		return RequestLog{}, err
	}
	entry.SubscriptionID = nullInt64Ptr(subscriptionID)
	entry.APIKeyID = nullInt64Ptr(apiKeyID)
	entry.SubscriptionName = subscriptionName.String
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
	var subscriptionID, apiKeyID, input, output, cacheRead, cacheCreation, total sql.NullInt64
	var subscriptionName, apiKeyName, prefix, provider, query, clientIP, requestID, errorType, model sql.NullString
	var requestHeaders, requestBody, responseHeaders, responseBody sql.NullString
	var started, finished string
	var requestTrunc, responseTrunc int
	if err := s.Scan(&entry.Day, &entry.ID, &subscriptionID, &subscriptionName, &apiKeyID, &apiKeyName, &prefix, &provider,
		&entry.Method, &entry.Path, &query, &clientIP, &entry.StatusCode, &started, &finished, &entry.DurationMs, &requestID, &errorType,
		&model, &input, &output, &cacheRead, &cacheCreation, &total,
		&requestHeaders, &requestBody, &responseHeaders, &responseBody, &requestTrunc, &responseTrunc); err != nil {
		return RequestLog{}, err
	}
	entry.SubscriptionID = nullInt64Ptr(subscriptionID)
	entry.APIKeyID = nullInt64Ptr(apiKeyID)
	entry.SubscriptionName = subscriptionName.String
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
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized time %q", value)
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
