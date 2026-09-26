package database

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// NewDatabase creates a database implementation from a database URL. The
// returned Database is a MemoryDatabase that caches the small records and
// delegates durable storage to the SQLite or PostgreSQL store selected by the
// URL. Supported schemes are SQLite, postgres, and postgresql. Examples:
//
//	sqlite::memory:
//	sqlite://./data/app.db
//	sqlite:///var/lib/app/data.db
//	postgresql://user:password@localhost:5432/ai_unisub?sslmode=disable
func NewDatabase(databaseURL string) (Database, error) {
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" {
		return nil, errDatabaseURLSchemeRequired
	}
	switch {
	case strings.EqualFold(parsed.Scheme, "sqlite"):
		path, err := sqlitePathFromURL(parsed, databaseURL)
		if err != nil {
			return nil, err
		}
		return newMemoryDatabase(NewSQLiteDatabase(path)), nil
	case strings.EqualFold(parsed.Scheme, "postgres"), strings.EqualFold(parsed.Scheme, "postgresql"):
		return newMemoryDatabase(NewPostgreSQLDatabase(databaseURL)), nil
	default:
		return nil, fmt.Errorf("%w: %s", errUnsupportedDatabaseScheme, parsed.Scheme)
	}
}

func sqlitePathFromURL(parsed *url.URL, rawURL string) (string, error) {
	if parsed.Opaque != "" {
		if parsed.Opaque == ":memory:" {
			return parsed.Opaque, nil
		}
		return url.PathUnescape(parsed.Opaque)
	}

	path := parsed.Path
	if parsed.Host != "" {
		path = parsed.Host + path
	}
	path, err := url.PathUnescape(path)
	if err != nil {
		return "", err
	}
	if len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		// sqlite:///C:/data/app.db is a valid URL spelling for a Windows path.
		path = path[1:]
	}
	if parsed.RawQuery != "" {
		path += "?" + parsed.RawQuery
	}
	if path == "" {
		return "", errSQLiteDatabasePathRequired
	}
	if rawURL == "sqlite://:memory:" || path == ":memory:" || path == "/:memory:" {
		return ":memory:", nil
	}
	return path, nil
}

// Finalize fills TotalTokens from the four token counters.
func (t *UsageTotals) Finalize() {
	t.TotalTokens = t.InputTokens + t.OutputTokens + t.CacheCreationTokens + t.CacheReadTokens
}

func (t *UsageTotals) add(other UsageTotals) {
	t.Requests += other.Requests
	t.InputTokens += other.InputTokens
	t.OutputTokens += other.OutputTokens
	t.CacheCreationTokens += other.CacheCreationTokens
	t.CacheReadTokens += other.CacheReadTokens
	t.Finalize()
}

func validateTimeRange(r TimeRange) error {
	if r.Start.IsZero() || r.End.IsZero() {
		return errTimeRangeRequired
	}
	if r.Start.After(r.End) {
		return errTimeRangeInvalid
	}
	return nil
}

// Database is the persistence contract. MemoryDatabase implements it: it owns
// validation and the record cache while SQLiteDatabase or PostgreSQLDatabase
// executes the SQL. Tests can use the sqlite::memory: URL for a process-local
// database.
// Account configurations are instance records, not configuration records
// for a provider type.
type Database interface {
	// Open initializes the database and makes it ready for use.
	Open() error
	// Close releases the database resources.
	Close() error

	// ListConfigs returns all generic configuration objects, ordered by ID.
	ListConfigs() ([]PersistedConfig, error)
	// ListConfigsByType returns configuration objects of the given type, ordered by ID.
	ListConfigsByType(configType string) ([]PersistedConfig, error)
	// LoadConfig loads one configuration by type and name.
	// Missing configurations return a zero-value PersistedConfig (ID == 0) and a nil error.
	LoadConfig(configType, name string) (PersistedConfig, error)
	// SaveConfig creates (ID == 0) or updates (ID > 0) a configuration object.
	// Updates may only change Value; Type and Name are immutable after create.
	SaveConfig(config *PersistedConfig) error
	// DeleteConfig deletes a configuration object by ID.
	DeleteConfig(id int) error

	// ListProxyGroups returns all persisted proxy groups.
	ListProxyGroups() ([]PersistedProxyGroup, error)
	// SaveProxyGroup creates or updates a persisted proxy group.
	SaveProxyGroup(group *PersistedProxyGroup) error
	// DeleteProxyGroup deletes a proxy group by ID.
	DeleteProxyGroup(id int) error
	// RecordProxyLog persists one proxy log entry.
	RecordProxyLog(log *PersistedProxyLog) error
	// QueryProxyLogs returns proxy logs matching filter, with one-based pagination.
	QueryProxyLogs(filter ProxyLogFilter, page, pageSize int) ([]PersistedProxyLog, int, error)
	// CleanupProxyLog removes proxy logs older than the specified number of days.
	CleanupProxyLog(days int) error

	// ListAccounts returns all persisted accounts.
	ListAccounts() ([]PersistedAccount, error)
	// SaveAccount creates or updates a persisted account.
	SaveAccount(account *PersistedAccount) error
	// DeleteAccount deletes an account by ID.
	DeleteAccount(id int) error

	// ListUsers returns all persisted users.
	ListUsers() ([]PersistedUser, error)
	// SaveUser creates or updates a persisted user.
	SaveUser(user *PersistedUser) error
	// DeleteUser deletes a user by ID.
	DeleteUser(id int) error

	// ListAPIKeys returns API keys belonging to the specified user.
	// userID 0 returns the keys of every user.
	ListAPIKeys(userID int) ([]PersistedAPIKey, error)
	// SaveAPIKey creates or updates a persisted API key.
	SaveAPIKey(key *PersistedAPIKey) error
	// DeleteAPIKey deletes an API key by ID.
	DeleteAPIKey(id int) error

	// RecordCallTrace persists one complete API call trace.
	RecordCallTrace(trace *PersistedCallTrace) error
	// CleanupCallTrace removes call-trace data older than the specified number of days.
	CleanupCallTrace(days int) error
	// QueryCallTraces returns call-trace summaries matching filter, with one-based pagination.
	// filter.TimeRange start and end are required; the database does not cap the window.
	QueryCallTraces(filter CallTraceFilter, page, pageSize int) ([]PersistedCallTraceSummary, int, error)
	// GetCallTrace returns the complete trace, including request/response bodies
	// and headers. finishedAt identifies the UTC day containing the trace.
	GetCallTrace(finishedAt time.Time, id int) (*PersistedCallTrace, error)

	// QueryAccountUsage aggregates call-trace usage by account_id in timeRange.
	// Aggregation runs in SQL over call-trace storage; no row bodies are loaded.
	QueryAccountUsage(timeRange TimeRange) ([]AccountUsageRow, UsageTotals, error)
	// QueryUserUsage aggregates call-trace usage by API-key owner (user_id) in timeRange.
	// accountID 0 means all accounts; otherwise only that account's traces are included.
	// UserID 0 groups traces whose apikey does not match any stored key.
	QueryUserUsage(timeRange TimeRange, accountID int) ([]UserUsageRow, UsageTotals, error)
}
