package oauth

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

type XAIConfig struct {
	Issuer        string
	ClientID      string
	ClientVersion string
	Scopes        []string
	HTTPClient    *http.Client
}

type XAIAdapter struct {
	config XAIConfig
}

func NewXAI(config XAIConfig) *XAIAdapter {
	config.Issuer = cmp.Or(config.Issuer, "https://auth.x.ai")
	config.ClientID = cmp.Or(config.ClientID, "b1a00492-073a-47ea-816f-4c329264a828")
	config.ClientVersion = cmp.Or(config.ClientVersion, "0.2.114")
	if len(config.Scopes) == 0 {
		config.Scopes = []string{
			"openid", "profile", "email", "offline_access", "grok-cli:access", "api:access",
			"conversations:read", "conversations:write", "workspaces:read", "workspaces:write",
		}
	} else {
		config.Scopes = slices.Clone(config.Scopes)
	}
	return &XAIAdapter{config: config}
}

func (a *XAIAdapter) Service() string { return OAuthServiceXAI }

func (a *XAIAdapter) StartDeviceAuthorization(ctx context.Context, input DeviceStartInput) (DeviceAuthorizationResult, error) {
	values := url.Values{
		"client_id": {a.config.ClientID}, "scope": {strings.Join(a.config.Scopes, " ")}, "referrer": {"ai-unisub2"},
	}
	request, err := a.request(ctx, strings.TrimRight(a.config.Issuer, "/")+"/oauth/device/code", values)
	if err != nil {
		return DeviceAuthorizationResult{}, err
	}
	var response struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := doOAuthRequest(selectHTTPClient(a.config.HTTPClient, input.HTTPClient), request, &response); err != nil {
		return DeviceAuthorizationResult{}, err
	}
	if response.DeviceCode == "" {
		return DeviceAuthorizationResult{}, errXAIDeviceAuthorizationNoDeviceCode
	}
	interval := time.Duration(response.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	var expiresAt time.Time
	if response.ExpiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(response.ExpiresIn) * time.Second).UTC()
	}
	return DeviceAuthorizationResult{
		DeviceCode: response.DeviceCode, UserCode: response.UserCode,
		VerificationURI: cmp.Or(response.VerificationURIComplete, response.VerificationURI),
		ExpiresAt:       expiresAt, Interval: interval,
	}, nil
}

func (a *XAIAdapter) PollDeviceToken(ctx context.Context, deviceCode string, client *http.Client) (*OAuthCredential, error) {
	if deviceCode == "" {
		return nil, errDeviceCodeRequired
	}
	values := url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {deviceCode}, "client_id": {a.config.ClientID},
	}
	credential, err := a.token(ctx, values, client)
	if requestErr, ok := errors.AsType[*oauthRequestError](err); ok {
		switch requestErr.Code {
		case "authorization_pending":
			return nil, ErrAuthorizationPending
		case "slow_down":
			return nil, ErrSlowDown
		}
	}
	return credential, err
}

func (a *XAIAdapter) Refresh(ctx context.Context, credential *OAuthCredential, client *http.Client) (*OAuthCredential, error) {
	if credential == nil || credential.RefreshToken == "" {
		return nil, errCredentialNoRefreshToken
	}
	return a.token(ctx, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {credential.RefreshToken}, "client_id": {a.config.ClientID},
	}, client)
}

func (a *XAIAdapter) token(ctx context.Context, values url.Values, client *http.Client) (*OAuthCredential, error) {
	request, err := a.request(ctx, strings.TrimRight(a.config.Issuer, "/")+"/oauth/token", values)
	if err != nil {
		return nil, err
	}
	var token tokenResponse
	if err := doOAuthRequest(selectHTTPClient(a.config.HTTPClient, client), request, &token); err != nil {
		return nil, err
	}
	return credentialFromToken(token)
}

func (a *XAIAdapter) request(ctx context.Context, endpoint string, values url.Values) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "xai-grok-workspace/"+a.config.ClientVersion)
	request.Header.Set("x-grok-client-version", a.config.ClientVersion)
	request.Header.Set("x-grok-client-surface", "ui")
	return request, nil
}
