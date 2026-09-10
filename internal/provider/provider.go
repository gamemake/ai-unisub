// Package docs_interface contains the contracts shared by the API layer and
// the provider/database implementations.
package provider

import (
	"context"
	"encoding/json"
	"net/http"
)

// ProviderConfig is the configuration of one concrete provider instance.
//
// A provider is a transparent HTTP forwarder. Only authentication-related
// request headers may be replaced or added; the request body, URL, method,
// query, and all other headers must be forwarded unchanged.
type ProviderConfig struct {
	ID                       string   `json:"id"`
	Name                     string   `json:"name"`
	CredentialID             string   `json:"credential_id,omitempty"`
	Labels                   []string `json:"labels"`
	Proxy                    string   `json:"proxy"`
	Enabled                  bool     `json:"enabled"`
	MaxConcurrentConnections int      `json:"max_concurrent_connections"`
}

// UsageItem is one ordered name/value pair returned by a provider.
type UsageItem struct {
	Name  string
	Value string
}

// ProviderCallTrace contains the complete request/response data collected by a
// Provider. It is owned by the provider layer and deliberately contains no
// database or caller-authentication metadata. The outer request layer may
// translate it into its own persistence model.
// Header and body values are kept raw so recorders can inspect the original
// data without relying on a provider-specific representation.
type ProviderCallTrace struct {
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
type APICallRecorder func(*ProviderCallTrace)

// Provider forwards an HTTP request to a subscription-backed service.
// Implementations must preserve the request and response as-is, apart from
// authentication-related request headers.
type Provider interface {
	Config() ProviderConfig
	// UpdateConfig updates mutable provider-specific settings. The ProviderConfig.ID
	// value is assigned at creation time and must not be changed.
	UpdateConfig(json.RawMessage) error
	Handle(*http.Request, APICallRecorder)
	FetchUsage(context.Context) ([]UsageItem, error)
	ResetUsage(context.Context) error
}
