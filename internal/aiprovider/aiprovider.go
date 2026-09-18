// Package aiprovider implements AI upstreams and their request/response contracts.
package aiprovider

import (
	"ai-unisub/internal/proxy"
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type ProxyResolver interface {
	ResolveProxy(ctx context.Context, groupID int, appType string, z []string) (*proxy.Endpoint, error)
	ProxyRetryLimit(groupID int) int
	ReportProxy(ep *proxy.Endpoint, appType string, eclass proxy.ErrorClass) error
}

// AIProviderConfig is the configuration of one concrete provider instance.
//
// Forwarding preserves the request body and model names; authentication and
// gateway-only headers are handled separately from client access policies.
type AIProviderConfig struct {
	Kind                          string        `json:"kind,omitempty"`
	Supplier                      string        `json:"supplier,omitempty"`
	ClientType                    ClientType    `json:"client_type"`
	OfficialOnly                  bool          `json:"official_only,omitzero"`
	Members                       []GroupMember `json:"members,omitempty"`
	ProxyApplicationErrorStatuses []int         `json:"proxy_application_error_statuses,omitempty"`
	ID                            int           `json:"id"`
	Name                          string        `json:"name"`
	Labels                        []string      `json:"labels"`
	ProxyGroupID                  int           `json:"proxy_group_id,omitempty"`
	APIEndpoint                   string        `json:"api_endpoint"`
	Enabled                       bool          `json:"enabled"`
	MaxConcurrentConnections      int           `json:"max_concurrent_connections"`
	QueueTimeoutSeconds           int           `json:"queue_timeout_seconds"`
	AuthType                      string        `json:"auth_type"`
	CredentialID                  string        `json:"credential_id,omitempty"`
	APIKey                        string        `json:"api_key,omitempty"`
}

// AIProviderState contains provider-owned state that is safe to persist.
// Runtime coordination state is deliberately kept out of this value.
type AIProviderState struct{}

// AIProviderQuota is the serializable quota snapshot owned by a provider.
type AIProviderQuota struct {
	Subscription []SubscriptionQuotaItem `json:"subscription,omitempty"`
	Items        []QuotaItem             `json:"items,omitempty"`
	CacheStatus  QuotaCacheStatus        `json:"cache_status"`
	UpdatedAt    time.Time               `json:"updated_at,omitzero"`
}

// ProviderData is the persistence boundary between the database layer and a
// provider. The aiprovider package owns the meaning of all three payloads.
type ProviderData struct {
	Config json.RawMessage `json:"config,omitempty"`
	State  json.RawMessage `json:"state,omitempty"`
	Quota  json.RawMessage `json:"quota,omitempty"`
}

// AIProviderCallTrace contains the complete request/response data collected by a
// AIProvider. It is owned by the provider layer and deliberately contains no
// database or caller-authentication metadata. The outer request layer may
// translate it into its own persistence model.
// Header and body values are kept raw so recorders can inspect the original
// data without relying on a provider-specific representation.
type AIProviderCallTrace struct {
	RetrySafe              bool // No upstream execution occurred; only pre-send failures set this.
	ResponseStatus         int
	URL                    string
	HTTPErrorCode          int
	HTTPErrorInfo          string
	OriginalRequestHeaders http.Header
	OutboundRequestHeaders http.Header
	RequestBody            []byte
	ResponseHeaders        http.Header
	ResponseBody           []byte
	Model                  string
	InputTokens            int
	OutputTokens           int
	CacheCreationTokens    int
	CacheReadTokens        int
}

// APICallRecorder receives the complete provider result after a request has
// been handled. The caller decides whether and how to persist the result.
type APICallRecorder func(*AIProviderCallTrace)

// AIProvider forwards an HTTP request to a subscription-backed service.
// Implementations preserve the upstream protocol.
type AIProvider interface {
	Config() AIProviderConfig
	State() AIProviderState
	Quota() AIProviderQuota
	// UpdateConfig updates mutable provider-specific settings. The AIProviderConfig.ID
	// value is assigned at creation time and must not be changed.
	UpdateConfig(json.RawMessage) error
	RestoreState(json.RawMessage) error
	RestoreQuota(json.RawMessage) error
	Handle(*http.Request, APICallRecorder)
	// FetchQuota queries current subscription usage or non-subscription balance.
	// Groups return ErrQuotaUnsupported. Historical usage/costs and cached fallback are excluded.
	FetchQuota(context.Context) (*Quota, error)
	// GetCachedQuota only reads the cache, without network or token refresh.
	// Groups return nil because they do not own quota.
	// It always returns a non-nil result, with missing status on a cache miss.
	GetCachedQuota() *Quota
	ResetUsage(context.Context) error
}
