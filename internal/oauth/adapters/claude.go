package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"ai-unisub/internal/oauth"
)

type ClaudeConfig struct {
	AuthorizeURL string
	TokenURL     string
	ClientID     string
	Scopes       []string
	HTTPClient   *http.Client
}

type ClaudeAdapter struct{ config ClaudeConfig }

func NewClaude(config ClaudeConfig) *ClaudeAdapter {
	if config.AuthorizeURL == "" {
		config.AuthorizeURL = "https://claude.com/cai/oauth/authorize"
	}
	if config.TokenURL == "" {
		config.TokenURL = "https://platform.claude.com/v1/oauth/token"
	}
	if config.ClientID == "" {
		config.ClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	}
	if len(config.Scopes) == 0 {
		config.Scopes = []string{"org:create_api_key", "user:profile", "user:inference", "user:sessions:claude_code", "user:mcp_servers", "user:file_upload"}
	}
	if config.HTTPClient == nil {
		config.HTTPClient = defaultHTTPClient()
	}
	return &ClaudeAdapter{config: config}
}
func (a *ClaudeAdapter) Service() string { return oauth.OAuthServiceClaude }
func (a *ClaudeAdapter) BuildAuthorizationURL(_ context.Context, in oauth.AuthorizationInput) (oauth.AuthorizationResult, error) {
	v := url.Values{"code": {"true"}, "client_id": {a.config.ClientID}, "response_type": {"code"}, "redirect_uri": {in.RedirectURI}, "scope": {strings.Join(a.config.Scopes, " ")}, "code_challenge": {challenge(in.CodeVerifier)}, "code_challenge_method": {"S256"}, "state": {in.State}}
	return oauth.AuthorizationResult{AuthorizationURL: a.config.AuthorizeURL + "?" + v.Encode(), ExpiresAt: nowPlus(10)}, nil
}
func (a *ClaudeAdapter) Exchange(ctx context.Context, code, state, verifier, redirect string) (*oauth.OAuthCredential, error) {
	return a.token(ctx, map[string]string{"grant_type": "authorization_code", "code": code, "redirect_uri": redirect, "client_id": a.config.ClientID, "code_verifier": verifier, "state": state})
}
func (a *ClaudeAdapter) Refresh(ctx context.Context, old *oauth.OAuthCredential) (*oauth.OAuthCredential, error) {
	result, err := a.token(ctx, map[string]string{"grant_type": "refresh_token", "refresh_token": old.RefreshToken, "client_id": a.config.ClientID})
	if err != nil {
		return nil, err
	}
	if result.RefreshToken == "" {
		result.RefreshToken = old.RefreshToken
	}
	result.AccountID, result.AccountName, result.Email = old.AccountID, old.AccountName, old.Email
	return result, nil
}
func (a *ClaudeAdapter) token(ctx context.Context, fields map[string]string) (*oauth.OAuthCredential, error) {
	body, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.config.TokenURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "claude-cli/2.1.220 (external, cli)")
	var token tokenResponse
	if _, err := readResponseDo(a.config.HTTPClient, req, &token); err != nil {
		return nil, err
	}
	return credential(token)
}
