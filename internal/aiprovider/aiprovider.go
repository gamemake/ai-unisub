// Package aiprovider implements AI upstreams and their request/response contracts.
package aiprovider

import (
	"ai-unisub/internal/proxy"
	"context"
	"encoding/json"
	"net/http"
)

type ProxyResolver interface {
	ResolveProxy(context.Context, string, string, []string) (*proxy.Endpoint, error)
	ProxyRetryLimit(string) int
	ReportProxy(*proxy.Endpoint, string, proxy.ErrorClass) error
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
	ID                            string        `json:"id"`
	Name                          string        `json:"name"`
	Labels                        []string      `json:"labels"`
	ProxyGroupID                  string        `json:"proxy_group_id,omitempty"`
	APIEndpoint                   string        `json:"api_endpoint"`
	Enabled                       bool          `json:"enabled"`
	MaxConcurrentConnections      int           `json:"max_concurrent_connections"`
	QueueTimeoutSeconds           int           `json:"queue_timeout_seconds"`
	AuthType                      string        `json:"auth_type"`
	CredentialID                  string        `json:"credential_id,omitempty"`
	APIKey                        string        `json:"api_key,omitempty"`
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
	// UpdateConfig updates mutable provider-specific settings. The AIProviderConfig.ID
	// value is assigned at creation time and must not be changed.
	UpdateConfig(json.RawMessage) error
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
