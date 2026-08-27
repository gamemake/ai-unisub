package model

import (
	"encoding/json"
	"time"
)

type Provider string

const (
	ProviderClaude Provider = "claude"
	ProviderCodex  Provider = "codex"
	ProviderGrok   Provider = "grok"
)

func (p Provider) Valid() bool {
	return p == ProviderClaude || p == ProviderCodex || p == ProviderGrok
}

type Account struct {
	ID                             int64           `json:"id"`
	Name                           string          `json:"name"`
	Provider                       Provider        `json:"provider"`
	AuthType                       string          `json:"auth_type"`
	Metadata                       json.RawMessage `json:"metadata"`
	Status                         string          `json:"status"`
	Enabled                        bool            `json:"enabled"`
	ConcurrencyLimit               int             `json:"concurrency_limit"`
	ConcurrencyQueueTimeoutSeconds int             `json:"concurrency_queue_timeout_seconds"`
	ProxyConfigured                bool            `json:"proxy_configured"`
	TokenExpiresAt                 *time.Time      `json:"token_expires_at,omitempty"`
	Quota                          json.RawMessage `json:"quota,omitempty"`
	QuotaCheckedAt                 *time.Time      `json:"quota_checked_at,omitempty"`
	QuotaError                     *string         `json:"quota_error,omitempty"`
	LastUsedAt                     *time.Time      `json:"last_used_at,omitempty"`
	LastError                      *string         `json:"last_error,omitempty"`
	APIKeyCount                    int             `json:"api_key_count"`
	CreatedByUserID                *int64          `json:"created_by_user_id,omitempty"`
	CreatedAt                      time.Time       `json:"created_at"`
	UpdatedAt                      time.Time       `json:"updated_at"`
	Credentials                    json.RawMessage `json:"credentials,omitempty"`
	CredentialsJSON                []byte          `json:"-"`
	ProxyURL                       string          `json:"-"`
}

type APIKey struct {
	ID          int64      `json:"id"`
	AccountID   int64      `json:"account_id"`
	AccountName string     `json:"account_name"`
	Provider    Provider   `json:"provider"`
	Name        string     `json:"name"`
	KeyPrefix   string     `json:"key_prefix"`
	APIKey      string     `json:"api_key,omitempty"`
	Enabled     bool       `json:"enabled"`
	RPMLimit    *int       `json:"rpm_limit,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

type Credentials struct {
	AccessToken      string `json:"access_token,omitempty"`
	RefreshToken     string `json:"refresh_token,omitempty"`
	IDToken          string `json:"id_token,omitempty"`
	APIKey           string `json:"api_key,omitempty"`
	TokenType        string `json:"token_type,omitempty"`
	ChatGPTAccountID string `json:"chatgpt_account_id,omitempty"`
	OrganizationID   string `json:"organization_id,omitempty"`
	TeamID           string `json:"team_id,omitempty"`
	ClientID         string `json:"client_id,omitempty"`
	Scope            string `json:"scope,omitempty"`
	UserID           string `json:"user_id,omitempty"`
	Email            string `json:"email,omitempty"`
}

func (c Credentials) Bearer() string {
	if c.AccessToken != "" {
		return c.AccessToken
	}
	return c.APIKey
}

type ResolvedAccount struct {
	Account
	APIKeyID int64
	RPMLimit *int `json:"-"`
}

type UsageSummary struct {
	Requests24H            int64 `json:"requests_24h"`
	InputTokens24H         int64 `json:"input_tokens_24h"`
	OutputTokens24H        int64 `json:"output_tokens_24h"`
	CacheReadTokens24H     int64 `json:"cache_read_tokens_24h"`
	CacheCreationTokens24H int64 `json:"cache_creation_tokens_24h"`
	TotalTokens24H         int64 `json:"total_tokens_24h"`
}

type UserRole string

const (
	RoleAdmin UserRole = "admin"
	RoleUser  UserRole = "user"
)

func (r UserRole) Valid() bool {
	return r == RoleAdmin || r == RoleUser
}

type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	Role         UserRole  `json:"role"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	PasswordHash string    `json:"-"`
}
