package database

import (
	"encoding/json"
	"net/http"
	"time"
)

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

// ModuleConfigType identifies legacy whole-module configuration documents
// stored as generic PersistedConfig rows.
const ModuleConfigType = "module"

// PersistedProxyLog records one outbound HTTP attempt. HTTPErrorCode contains
// the response status (zero for transport failures), despite its legacy name.
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
// CallTrace. The layer that coordinates a request should map the provider
// result into this type and add the request metadata that only the outer
// layer knows, such as the authenticated user API key and selected provider
// instance.
//
// APIKey contains the API key value used for the call.
type PersistedCallTrace struct {
	ID int `json:"id"`

	UserID         int    `json:"user_id"`
	APIKey         string `json:"apikey"`
	AIProviderType string `json:"provider_type"`
	AccountID      int    `json:"account_id"`
	RequestID      string `json:"request_id"`
	SessionID      string `json:"session_id"`
	SourceIP       string `json:"source_ip"`

	URL                    string      `json:"url"`
	RequestMethod          string      `json:"request_method"`
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
	QueueDurationMs        int64       `json:"queue_duration_ms"`
	RequestDurationMs      int64       `json:"request_duration_ms"`

	Model               string `json:"model"` // 只需要记录发给上游的模型名字
	InputTokens         int    `json:"input_tokens"`
	OutputTokens        int    `json:"output_tokens"`
	CacheCreationTokens int    `json:"cache_creation_tokens"`
	CacheReadTokens     int    `json:"cache_read_tokens"`

	FinishedAt time.Time `json:"finished_at"`
}

// PersistedCallTraceSummary is the lightweight representation returned by
// list queries. Body and Header fields are intentionally omitted so that list
// pages do not load large request/response payloads into memory.
type PersistedCallTraceSummary struct {
	ID                  int       `json:"id"`
	UserID              int       `json:"user_id"`
	APIKey              string    `json:"apikey"`
	AIProviderType      string    `json:"provider_type"`
	AccountID           int       `json:"account_id"`
	RequestID           string    `json:"request_id"`
	SessionID           string    `json:"session_id"`
	SourceIP            string    `json:"source_ip"`
	Username            string    `json:"username"`
	URL                 string    `json:"url"`
	RequestMethod       string    `json:"request_method"`
	OutboundURL         string    `json:"outbound_url"`
	HTTPErrorCode       int       `json:"http_error_code"`
	HTTPErrorInfo       string    `json:"http_error_info,omitempty"`
	Model               string    `json:"model"`
	InputTokens         int       `json:"input_tokens"`
	OutputTokens        int       `json:"output_tokens"`
	CacheCreationTokens int       `json:"cache_creation_tokens"`
	CacheReadTokens     int       `json:"cache_read_tokens"`
	FinishedAt          time.Time `json:"finished_at"`
	QueueDurationMs     int64     `json:"queue_duration_ms"`
	RequestDurationMs   int64     `json:"request_duration_ms"`
}

// CallTraceFilter combines exact search and structured filters for call traces.
type CallTraceFilter struct {
	UserID          *int      // Optional exact user ID filter; nil means no user filter, zero means unattributed calls.
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
