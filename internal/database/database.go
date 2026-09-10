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

// PersistedAccount stores a provider instance and its serialized configuration.
type PersistedAccount struct {
	ID        string          `json:"id"`
	Provider  string          `json:"provider"`
	Name      string          `json:"name"`
	Config    json.RawMessage `json:"config"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// PersistedUser represents a user who can create API keys.
type PersistedUser struct {
	ID           string    `json:"id"`
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
type PersistedAPIKey struct {
	ID           string    `json:"id"`
	UserID       string    `json:"user_id"`
	AccountID    string    `json:"account_id"`
	Key          string    `json:"key"`
	ValidSeconds int64     `json:"valid_seconds"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// PersistedCallTrace is the database-owned representation of one API call.
//
// This type is intentionally separate from the provider package's
// ProviderCallTrace. The layer that coordinates a request should map the provider
// result into this type and add the request metadata that only the outer
// layer knows, such as the authenticated user API key and selected provider
// instance.
//
// APIKey contains the API key value used for the call.
type PersistedCallTrace struct {
	ID string `json:"id"`

	APIKey       string `json:"apikey"`
	ProviderType string `json:"provider_type"`
	AccountID    string `json:"account_id"`
	RequestID    string `json:"request_id"`
	SourceIP     string `json:"source_ip"`

	URL                    string      `json:"url"`
	HTTPErrorCode          int         `json:"http_error_code"`
	HTTPErrorInfo          string      `json:"http_error_info,omitempty"`
	OriginalRequestHeaders http.Header `json:"original_request_headers"`
	OutboundRequestHeaders http.Header `json:"outbound_request_headers"`
	RequestBody            []byte      `json:"request_body"`
	ResponseHeaders        http.Header `json:"response_headers"`
	ResponseBody           []byte      `json:"response_body"`

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
	ID                  string    `json:"id"`
	APIKey              string    `json:"apikey"`
	ProviderType        string    `json:"provider_type"`
	AccountID           string    `json:"account_id"`
	RequestID           string    `json:"request_id"`
	SourceIP            string    `json:"source_ip"`
	URL                 string    `json:"url"`
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

// TimeRange is an inclusive range of time values.
type TimeRange struct {
	Start time.Time
	End   time.Time
}

// Database is the persistence contract. Implementations may be SQLite,
// Postgres, or an in-memory store used by tests. Provider configurations are
// instance records, not configuration records for a provider type.
type Database interface {
	Open() error
	Close() error

	// CredentialStore-compatible methods. The database stores credentials as
	// opaque JSON; OAuth owns the domain model and its serialization.
	LoadCredential(string) (json.RawMessage, error)
	SaveCredential(string, json.RawMessage) error
	DeleteCredential(string) error

	ListAccounts() ([]PersistedAccount, error)
	SaveAccount(*PersistedAccount) error
	DeleteAccount(string) error

	ListUsers() ([]PersistedUser, error)
	SaveUser(*PersistedUser) error
	DeleteUser(string) error

	ListAPIKeys(userID string) ([]PersistedAPIKey, error)
	SaveAPIKey(*PersistedAPIKey) error
	DeleteAPIKey(string) error

	RecordCallTrace(*PersistedCallTrace) error
	// Cleanup removes CallTrace data older than the specified number of days.
	// Account, user, and API key data are not removed.
	CleanupCallTrace(days int) error
	// QueryCallTraces applies the optional user, provider, HTTP error code, and
	// inclusive UTC time range filters. page is one-based and pageSize is the
	// maximum number of traces returned. A nil timeRange means that the time
	// range is unbounded. The returned count is the total
	// number of matching traces before pagination. A trace matches when its
	// source IP, model, or request ID equals any value in values.
	QueryCallTraces(userName, providerName string, httpErrorCode *int, page, pageSize int, timeRange *TimeRange, values ...string) ([]PersistedCallTraceSummary, int, error)
	// GetCallTrace returns the complete trace, including request/response bodies
	// and headers. startedAt identifies the UTC daily table containing the trace.
	GetCallTrace(startedAt time.Time, id string) (*PersistedCallTrace, error)
}
