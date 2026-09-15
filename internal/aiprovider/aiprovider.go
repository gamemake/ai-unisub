// Package aiprovider implements AI upstreams and their request/response contracts.
package aiprovider

import (
	"context"
	"encoding/json"
	"net/http"
)

type ProxyResolver interface {
	ResolveProxy(context.Context, string) (string, error)
	ProxyRetryLimit(string) int
	ReportProxy(string, string, bool)
}

// AIProviderConfig is the configuration of one concrete provider instance.
//
// A provider is a transparent HTTP forwarder. Only authentication-related
// request headers may be replaced or added; the request body, URL, method,
// query, and all other headers must be forwarded unchanged.
type AIProviderConfig struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Labels       []string `json:"labels"`
	ProxyGroupID string   `json:"proxy_group_id,omitempty"`
	// Proxy is retained for backward-compatible configs; new configs should use ProxyGroupID.
	Proxy                    string `json:"proxy,omitempty"`
	APIEndpoint              string `json:"api_endpoint"`
	Enabled                  bool   `json:"enabled"`
	MaxConcurrentConnections int    `json:"max_concurrent_connections"`
	QueueTimeoutSeconds      int    `json:"queue_timeout_seconds"`
	AuthType                 string `json:"auth_type"`
	CredentialID             string `json:"credential_id,omitempty"`
	APIKey                   string `json:"api_key,omitempty"`
}

// UsageItem is one ordered name/value pair returned by a provider.
type UsageItem struct {
	Name  string
	Value string
}

// AIProviderCallTrace contains the complete request/response data collected by a
// AIProvider. It is owned by the provider layer and deliberately contains no
// database or caller-authentication metadata. The outer request layer may
// translate it into its own persistence model.
// Header and body values are kept raw so recorders can inspect the original
// data without relying on a provider-specific representation.
type AIProviderCallTrace struct {
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
// Implementations must preserve the request and response as-is, apart from
// authentication-related request headers.
type AIProvider interface {
	Config() AIProviderConfig
	// UpdateConfig updates mutable provider-specific settings. The AIProviderConfig.ID
	// value is assigned at creation time and must not be changed.
	UpdateConfig(json.RawMessage) error
	Handle(*http.Request, APICallRecorder)
	FetchUsage(context.Context) ([]UsageItem, error)
	ResetUsage(context.Context) error
}
