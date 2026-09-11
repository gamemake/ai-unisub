package adapters

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ai-unisub/internal/oauth"
)

type GrokConfig struct {
	Issuer, ClientID, ClientVersion string
	Scopes                          []string
	HTTPClient                      *http.Client
}
type GrokAdapter struct{ config GrokConfig }

func NewGrok(config GrokConfig) *GrokAdapter {
	if config.Issuer == "" {
		config.Issuer = "https://auth.x.ai"
	}
	if config.ClientID == "" {
		config.ClientID = "b1a00492-073a-47ea-816f-4c329264a828"
	}
	if config.ClientVersion == "" {
		config.ClientVersion = "0.2.114"
	}
	if len(config.Scopes) == 0 {
		config.Scopes = []string{
			"openid", "profile", "email", "offline_access", "grok-cli:access", "api:access",
			"conversations:read", "conversations:write", "workspaces:read", "workspaces:write",
		}
	}
	if config.HTTPClient == nil {
		config.HTTPClient = defaultHTTPClient()
	}
	return &GrokAdapter{config: config}
}
func (a *GrokAdapter) Service() string { return oauth.OAuthServiceGrok }
func (a *GrokAdapter) StartDeviceAuthorization(ctx context.Context, _ oauth.DeviceStartInput) (oauth.DeviceAuthorizationResult, error) {
	v := url.Values{
		"client_id": {a.config.ClientID},
		"scope":     {strings.Join(a.config.Scopes, " ")},
		"referrer":  {"ai-unisub2"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.config.Issuer, "/")+"/oauth2/device/code", strings.NewReader(v.Encode()))
	if err != nil {
		return oauth.DeviceAuthorizationResult{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "xai-grok-workspace/"+a.config.ClientVersion)
	req.Header.Set("x-grok-client-version", a.config.ClientVersion)
	req.Header.Set("x-grok-client-surface", "ui")
	var response struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		Expires                 int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	_, err = readResponseDo(clientWithProxy(ctx, a.config.HTTPClient), req, &response)
	if err != nil {
		return oauth.DeviceAuthorizationResult{}, err
	}
	if response.DeviceCode == "" {
		return oauth.DeviceAuthorizationResult{}, errors.New("Grok device authorization returned no device code")
	}
	uri := response.VerificationURIComplete
	if uri == "" {
		uri = response.VerificationURI
	}
	interval := time.Duration(response.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	var expiresAt time.Time
	if response.Expires > 0 {
		expiresAt = time.Now().Add(time.Duration(response.Expires) * time.Second).UTC()
	}
	return oauth.DeviceAuthorizationResult{DeviceCode: response.DeviceCode, UserCode: response.UserCode, VerificationURI: uri, ExpiresAt: expiresAt, Interval: interval}, nil
}
func (a *GrokAdapter) PollDeviceToken(ctx context.Context, deviceCode string) (*oauth.OAuthCredential, error) {
	v := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "device_code": {deviceCode}, "client_id": {a.config.ClientID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.config.Issuer, "/")+"/oauth2/token", strings.NewReader(v.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "xai-grok-workspace/"+a.config.ClientVersion)
	req.Header.Set("x-grok-client-version", a.config.ClientVersion)
	req.Header.Set("x-grok-client-surface", "ui")
	var token tokenResponse
	_, err = readResponseDo(clientWithProxy(ctx, a.config.HTTPClient), req, &token)
	if err != nil {
		if strings.Contains(err.Error(), "authorization_pending") {
			return nil, oauth.ErrAuthorizationPending
		}
		if strings.Contains(err.Error(), "slow_down") {
			return nil, oauth.ErrSlowDown
		}
		return nil, err
	}
	return credential(token)
}
func (a *GrokAdapter) Refresh(ctx context.Context, old *oauth.OAuthCredential) (*oauth.OAuthCredential, error) {
	v := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {old.RefreshToken}, "client_id": {a.config.ClientID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.config.Issuer, "/")+"/oauth2/token", strings.NewReader(v.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-grok-client-version", a.config.ClientVersion)
	req.Header.Set("x-grok-client-surface", "ui")
	var token tokenResponse
	if _, err := readResponseDo(clientWithProxy(ctx, a.config.HTTPClient), req, &token); err != nil {
		return nil, err
	}
	return credential(token)
}
