package aiprovider2

import (
	"ai-unisub/internal/oauth2"
	"time"
)

type ClientType string

const (
	ClientClaude ClientType = "claude"
	ClientCodex  ClientType = "codex"
	ClientGrok   ClientType = "grok"
)

type AccountKind string

const (
	AccountOAuth AccountKind = "oauth"
	AccountAPI   AccountKind = "api"
	AccountGroup AccountKind = "group"
)

type GroupMember struct {
	ID     int `json:"id"`
	Weight int `json:"weight"`
}

type AccountConfig struct {
	// 公共部分
	Kind                     AccountKind `json:"kind,omitempty"`
	Name                     string      `json:"name"`
	Labels                   []string    `json:"labels"`
	Supplier                 string      `json:"supplier,omitempty"`
	ClientType               ClientType  `json:"client_type,omitempty"`
	ProxyGroupID             int         `json:"proxy_group_id,omitempty"`
	Enabled                  bool        `json:"enabled"`
	MaxConcurrentConnections int         `json:"max_concurrent_connections"`
	QueueTimeoutSeconds      int         `json:"queue_timeout_seconds"`

	// 订阅独有
	SubscriptionPlan string                 `json:"subscription_plan,omitempty"`
	OfficialOnly     bool                   `json:"official_only,omitzero"`
	Credential       oauth2.OAuthCredential `json:"credential,omitzero"`

	// API 独有
	APIEndpoint string `json:"api_endpoint"`
	APIKey      string `json:"api_key,omitempty"`

	// Group 独有
	Members []GroupMember `json:"members,omitempty"`
}

type AccountState struct {
	ActiveConnections int       `json:"active_connections"`
	QueuedConnections int       `json:"queued_connections"`
	RefreshAt         time.Time `json:"refresh_at,omitzero"`
}

type SubscriptionQuotaItem struct {
	TimeDimension string    `json:"time_dimension"`
	Usage         float64   `json:"usage"`
	ResetAt       time.Time `json:"reset_at"`
}

type QuotaItem struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Source string `json:"source,omitempty"`
}

type QuotaCacheStatus string

const (
	QuotaCacheMissing QuotaCacheStatus = "missing"
	QuotaCacheFresh   QuotaCacheStatus = "fresh"
)

type AccountQuota struct {
	Subscription []SubscriptionQuotaItem `json:"subscription,omitempty"`
	Items        []QuotaItem             `json:"items,omitempty"`
	CacheStatus  QuotaCacheStatus        `json:"cache_status,omitempty"`
	UpdatedAt    time.Time               `json:"updated_at,omitzero"`
}
