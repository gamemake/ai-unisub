package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const (
	OAuthServiceGrok   = "grok"
	OAuthServiceCodex  = "codex"
	OAuthServiceClaude = "claude"
	OAuthServiceDummy  = "dummy"
)

var (
	ErrSessionNotFound      = errors.New("oauth session not found")
	ErrSessionExpired       = errors.New("oauth session expired")
	ErrStateMismatch        = errors.New("oauth state mismatch")
	ErrUnsupportedFlow      = errors.New("unsupported oauth flow")
	ErrAuthorizationPending = errors.New("oauth authorization pending")
	ErrSlowDown             = errors.New("oauth authorization polling should slow down")
)

// OAuthCredential contains the normalized OAuth result and provider-specific
// fields returned by the upstream service.
type OAuthCredential struct {
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`

	AccountID   string `json:"account_id,omitempty"`
	AccountName string `json:"account_name,omitempty"`
	Email       string `json:"email,omitempty"`
}

// CredentialStore persists the JSON representation of an OAuth credential.
// Keeping the store JSON-based prevents storage implementations from
// depending on OAuth domain types. The OAuth layer owns marshaling and
// unmarshaling OAuthCredential.
type CredentialStore interface {
	LoadCredential(string) (json.RawMessage, error)
	SaveCredential(string, json.RawMessage) error
	DeleteCredential(string) error
}

type OAuthAdapter interface {
	Service() string
	Refresh(context.Context, *OAuthCredential) (*OAuthCredential, error)
}

type AuthorizationInput struct {
	Service      string
	State        string
	CodeVerifier string
	RedirectURI  string
}

type AuthorizationResult struct {
	AuthorizationURL string
	UserCode         string
	VerificationURI  string
	ExpiresAt        time.Time
}

type PKCEAdapter interface {
	OAuthAdapter
	BuildAuthorizationURL(context.Context, AuthorizationInput) (AuthorizationResult, error)
	Exchange(ctx context.Context, code, state, codeVerifier, redirectURI string) (*OAuthCredential, error)
}

type DeviceStartInput struct {
	Service string
}

type DeviceAuthorizationResult struct {
	DeviceCode      string
	UserCode        string
	VerificationURI string
	ExpiresAt       time.Time
	Interval        time.Duration
}

type DeviceAdapter interface {
	OAuthAdapter
	StartDeviceAuthorization(context.Context, DeviceStartInput) (DeviceAuthorizationResult, error)
	PollDeviceToken(ctx context.Context, deviceCode string) (*OAuthCredential, error)
}

type RevocableAdapter interface {
	Revoke(context.Context, *OAuthCredential) error
}

type OAuthSession struct {
	ID           string
	Service      string
	SubjectID    string
	RedirectURI  string
	State        string
	CodeVerifier string
	DeviceCode   string
	Proxy        string
	ExpiresAt    time.Time
}

type StartResult struct {
	SessionID        string    `json:"session_id"`
	AuthorizationURL string    `json:"authorization_url"`
	UserCode         string    `json:"user_code,omitempty"`
	VerificationURI  string    `json:"verification_uri,omitempty"`
	ExpiresAt        time.Time `json:"expires_at"`
}
