package oauth2

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

type AnthropicConfig struct {
	AuthorizeURL string
	TokenURL     string
	ClientID     string
	Scopes       []string
	HTTPClient   *http.Client
}

type AnthropicAdapter struct {
	config AnthropicConfig
}

func NewAnthropic(config AnthropicConfig) *AnthropicAdapter {
	config.AuthorizeURL = cmp.Or(config.AuthorizeURL, "https://claude.com/cai/oauth/authorize")
	config.TokenURL = cmp.Or(config.TokenURL, "https://platform.claude.com/v1/oauth/token")
	config.ClientID = cmp.Or(config.ClientID, "9d1c250a-e61b-44d9-88ed-5944d1962f5e")
	if len(config.Scopes) == 0 {
		config.Scopes = []string{
			"org:create_api_key", "user:profile", "user:inference", "user:sessions:claude_code",
			"user:mcp_servers", "user:file_upload",
		}
	} else {
		config.Scopes = slices.Clone(config.Scopes)
	}
	return &AnthropicAdapter{config: config}
}

func (a *AnthropicAdapter) Service() string { return OAuthServiceAnthropic }

func (a *AnthropicAdapter) BuildAuthorizationURL(_ context.Context, input AuthorizationInput) (AuthorizationResult, error) {
	values := url.Values{
		"code": {"true"}, "client_id": {a.config.ClientID}, "response_type": {"code"},
		"redirect_uri": {input.RedirectURI}, "scope": {strings.Join(a.config.Scopes, " ")},
		"code_challenge": {pkceChallenge(input.CodeVerifier)}, "code_challenge_method": {"S256"},
		"state": {input.State},
	}
	return AuthorizationResult{AuthorizationURL: a.config.AuthorizeURL + "?" + values.Encode(), ExpiresAt: time.Now().Add(oauthSessionTTL)}, nil
}

func (a *AnthropicAdapter) Exchange(ctx context.Context, code, state, verifier, redirectURI string, client *http.Client) (*OAuthCredential, error) {
	return a.token(ctx, map[string]string{
		"grant_type": "authorization_code", "code": code, "redirect_uri": redirectURI,
		"client_id": a.config.ClientID, "code_verifier": verifier, "state": state,
	}, client)
}

func (a *AnthropicAdapter) Refresh(ctx context.Context, credential *OAuthCredential, client *http.Client) (*OAuthCredential, error) {
	if credential == nil || credential.RefreshToken == "" {
		return nil, errors.New("credential has no refresh token")
	}
	refreshed, err := a.token(ctx, map[string]string{
		"grant_type": "refresh_token", "refresh_token": credential.RefreshToken, "client_id": a.config.ClientID,
	}, client)
	if err != nil {
		return nil, err
	}
	refreshed.RefreshToken = cmp.Or(refreshed.RefreshToken, credential.RefreshToken)
	refreshed.AccountID = credential.AccountID
	refreshed.AccountName = credential.AccountName
	refreshed.Email = credential.Email
	return refreshed, nil
}

func (a *AnthropicAdapter) token(ctx context.Context, fields map[string]string, client *http.Client) (*OAuthCredential, error) {
	body, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.config.TokenURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "claude-cli/2.1.220 (external, cli)")
	var token tokenResponse
	if err := doOAuthRequest(selectHTTPClient(a.config.HTTPClient, client), request, &token); err != nil {
		return nil, err
	}
	return credentialFromToken(token)
}
