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
	APIKeyPrefix                   string          `json:"api_key_prefix"`
	CreatedAt                      time.Time       `json:"created_at"`
	UpdatedAt                      time.Time       `json:"updated_at"`
	CredentialsJSON                []byte          `json:"-"`
	ProxyURL                       string          `json:"-"`
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
	RPMLimit *int
}

type UsageSummary struct {
	Requests24H     int64 `json:"requests_24h"`
	InputTokens24H  int64 `json:"input_tokens_24h"`
	OutputTokens24H int64 `json:"output_tokens_24h"`
}
