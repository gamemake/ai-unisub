package database

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrCallTraceNotFound = errors.New("call trace not found")

// NewDatabase creates a database implementation from a database URL.
// SQLite is currently the only supported scheme. Examples:
//
//	sqlite::memory:
//	sqlite://./data/app.db
//	sqlite:///var/lib/app/data.db
func NewDatabase(databaseURL string) (Database, error) {
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" {
		return nil, errors.New("database URL must include a scheme")
	}
	if !strings.EqualFold(parsed.Scheme, "sqlite") {
		return nil, errors.New("unsupported database scheme: " + parsed.Scheme)
	}

	path, err := sqlitePathFromURL(parsed, databaseURL)
	if err != nil {
		return nil, err
	}
	return NewSQLiteDatabase(path), nil
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
		return "", errors.New("sqlite database URL must include a path")
	}
	if rawURL == "sqlite://:memory:" || path == ":memory:" || path == "/:memory:" {
		return ":memory:", nil
	}
	return path, nil
}

// Proxy persistence types belong to the database package. The egress package
// consumes these types through its persistence store contract.
type PersistedProxyGroup struct {
	ID        int             `json:"id"`
	Config    json.RawMessage `json:"config"`
	State     json.RawMessage `json:"state"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// PersistedConfig is a generic named configuration object.
// Value is opaque JSON; the caller owns schema validation.
// Type and Name form a unique identity and cannot change on update.
type PersistedConfig struct {
	ID    int             `json:"id"`
	Type  string          `json:"type"`
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value"`
}

// PersistedProxyLog records an upstream HTTP error for a proxy.
type PersistedProxyLog struct {
	GroupID          int       `json:"group_id"`
	ProxyURL         string    `json:"proxy_url"`
	URL              string    `json:"url"`
	AppType          string    `json:"app_type"`
	HTTPErrorCode    int       `json:"http_error_code"`
	HTTPErrorMessage string    `json:"http_error_message"`
	Time             time.Time `json:"time"`
}

// PersistedAccount stores a provider instance and its serialized configuration.
type PersistedAccount struct {
	ID         int             `json:"id"`
	AIProvider string          `json:"provider"`
	Name       string          `json:"name"`
	Config     json.RawMessage `json:"config"`
	State      json.RawMessage `json:"state"`
	Quota      json.RawMessage `json:"quota"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// PersistedUser represents a user who can create API keys.
type PersistedUser struct {
	ID           int       `json:"id"`
	Name         string    `json:"name"`
	Labels       []string  `json:"labels"`
	Role         UserRole  `json:"role"`
	Enabled      bool      `json:"enabled"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// UserRole defines the permissions level of a persisted user.
type UserRole string

const (
	UserRoleAdmin UserRole = "admin"
	UserRoleUser  UserRole = "user"
)

// PersistedAPIKey is created by a user and is bound to exactly one provider
// instance. Key stores the actual API key value.
// Config is opaque JSON owned by the caller.
type PersistedAPIKey struct {
	ID           int             `json:"id"`
	Name         string          `json:"name"`
	UserID       int             `json:"user_id"`
	AccountID    int             `json:"account_id"`
	Key          string          `json:"key"`
	Config       json.RawMessage `json:"config"`
	ValidSeconds int64           `json:"valid_seconds"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// PersistedCallTrace is the database-owned representation of one API call.
//
// This type is intentionally separate from the provider package's
// AIProviderCallTrace. The layer that coordinates a request should map the provider
// result into this type and add the request metadata that only the outer
// layer knows, such as the authenticated user API key and selected provider
// instance.
//
// APIKey contains the API key value used for the call.
type PersistedCallTrace struct {
	ID int `json:"id"`

	APIKey         string `json:"apikey"`
	AIProviderType string `json:"provider_type"`
	AccountID      int    `json:"account_id"`
	RequestID      string `json:"request_id"`
	SessionID      string `json:"session_id"`
	SourceIP       string `json:"source_ip"`

	URL                    string      `json:"url"`
	OutboundURL            string      `json:"outbound_url"`
	HTTPErrorCode          int         `json:"http_error_code"`
	HTTPErrorInfo          string      `json:"http_error_info,omitempty"`
	OriginalRequestHeaders http.Header `json:"original_request_headers"`
	OutboundRequestHeaders http.Header `json:"outbound_request_headers"`
	RequestBody            []byte      `json:"request_body"`
	ResponseHeaders        http.Header `json:"response_headers"`
	ResponseBody           []byte      `json:"response_body"`
	RequestBytes           int64       `json:"-"`
	ResponseBytes          int64       `json:"-"`

	Model               string `json:"model"`
	InputTokens         int    `json:"input_tokens"`
	OutputTokens        int    `json:"output_tokens"`
	CacheCreationTokens int    `json:"cache_creation_tokens"`
	CacheReadTokens     int    `json:"cache_read_tokens"`

	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

// PersistedCallTraceSummary is the lightweight representation returned by
// list queries. Body and Header fields are intentionally omitted so that list
// pages do not load large request/response payloads into memory.
type PersistedCallTraceSummary struct {
	ID                  int       `json:"id"`
	APIKey              string    `json:"apikey"`
	AIProviderType      string    `json:"provider_type"`
	AccountID           int       `json:"account_id"`
	RequestID           string    `json:"request_id"`
	SessionID           string    `json:"session_id"`
	SourceIP            string    `json:"source_ip"`
	URL                 string    `json:"url"`
	OutboundURL         string    `json:"outbound_url"`
	HTTPErrorCode       int       `json:"http_error_code"`
	HTTPErrorInfo       string    `json:"http_error_info,omitempty"`
	Model               string    `json:"model"`
	InputTokens         int       `json:"input_tokens"`
	OutputTokens        int       `json:"output_tokens"`
	CacheCreationTokens int       `json:"cache_creation_tokens"`
	CacheReadTokens     int       `json:"cache_read_tokens"`
	StartedAt           time.Time `json:"started_at"`
	FinishedAt          time.Time `json:"finished_at"`
}

// CallTraceFilter combines exact search and structured filters for call traces.
type CallTraceFilter struct {
	UserName        string    // Optional exact user-name filter.
	AccountID       int       // Optional exact account ID filter; zero means no filter.
	Code            *int      // Optional HTTP error-code filter.
	Search          string    // Optional exact text filter.
	SearchUsernames bool      // Optional; enables user-name search when authorized by the application layer.
	TimeRange       TimeRange // Required inclusive time range; start and end must be set.
}

// ProxyLogFilter combines filters for proxy logs.
type ProxyLogFilter struct {
	GroupID   int       // Required proxy-group ID filter.
	ProxyURL  string    // Optional exact proxy URL filter.
	AppType   string    // Optional exact application-type filter.
	TimeRange TimeRange // Required time range for the query.
}

// TimeRange is an inclusive range of time values.
type TimeRange struct {
	Start time.Time
	End   time.Time
}

// UsageTotals is aggregated call-trace usage for accounts or users.
type UsageTotals struct {
	Requests            int `json:"requests"`
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheCreationTokens int `json:"cache_creation_tokens"`
	CacheReadTokens     int `json:"cache_read_tokens"`
	TotalTokens         int `json:"total_tokens"`
}

// AccountUsageRow is per-account usage over a time range.
type AccountUsageRow struct {
	AccountID int         `json:"account_id"`
	Usage     UsageTotals `json:"usage"`
}

// UserUsageRow is per-user usage over a time range (UserID 0 = unattributed).
type UserUsageRow struct {
	UserID int         `json:"user_id"`
	Usage  UsageTotals `json:"usage"`
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
		return errors.New("start time and end time are required")
	}
	if r.Start.After(r.End) {
		return errors.New("start time must not be after end time")
	}
	return nil
}

// Database is the persistence contract. SQLiteDatabase is the concrete
// implementation; tests can use SQLiteDatabase with the :memory: path.
// AIProvider configurations are instance records, not configuration records
// for a provider type.
type Database interface {
	// Open initializes the database and makes it ready for use.
	Open() error
	// Close releases the database resources.
	Close() error

	// Module configurations are opaque JSON documents keyed by module name.
	// Missing configurations return nil, nil; modules own defaults and schema validation.
	// LoadModuleConfig loads a module configuration by module name.
	LoadModuleConfig(module string) (json.RawMessage, error)
	// SaveModuleConfig validates and stores a module configuration.
	SaveModuleConfig(module string, config json.RawMessage) error

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

	// CredentialStore-compatible methods. The database stores credentials as
	// opaque JSON; OAuth owns the domain model and its serialization.
	// LoadCredential loads an opaque credential by ID.
	LoadCredential(id string) (json.RawMessage, error)
	// SaveCredential creates or replaces an opaque credential by ID.
	SaveCredential(id string, value json.RawMessage) error
	// DeleteCredential deletes an opaque credential by ID.
	DeleteCredential(id string) error

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
	// and headers. startedAt identifies the UTC daily table containing the trace.
	GetCallTrace(startedAt time.Time, id int) (*PersistedCallTrace, error)

	// QueryAccountUsage aggregates call-trace usage by account_id in timeRange.
	// Aggregation runs in SQL over daily call_traces tables; no row bodies are loaded.
	QueryAccountUsage(timeRange TimeRange) ([]AccountUsageRow, UsageTotals, error)
	// QueryUserUsage aggregates call-trace usage by API-key owner (user_id) in timeRange.
	// accountID 0 means all accounts; otherwise only that account's traces are included.
	// UserID 0 groups traces whose apikey does not match any stored key.
	QueryUserUsage(timeRange TimeRange, accountID int) ([]UserUsageRow, UsageTotals, error)
}
