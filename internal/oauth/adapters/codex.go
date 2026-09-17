package adapters

import (
	"cmp"
	"context"
	"net/http"
	"net/url"
	"strings"

	"ai-unisub/internal/oauth"
)

type CodexConfig struct {
	AuthorizeURL, TokenURL, ClientID string
	Scopes                           []string
	HTTPClient                       *http.Client
}
type CodexAdapter struct{ config CodexConfig }

func NewCodex(config CodexConfig) *CodexAdapter {
	config.AuthorizeURL = cmp.Or(config.AuthorizeURL, "https://auth.openai.com/oauth/authorize")
	config.TokenURL = cmp.Or(config.TokenURL, "https://auth.openai.com/oauth/token")
	config.ClientID = cmp.Or(config.ClientID, "app_EMoamEEZ73f0CkXaXp7hrann")
	if len(config.Scopes) == 0 {
		config.Scopes = []string{"openid", "profile", "email", "offline_access"}
	}
	if config.HTTPClient == nil {
		config.HTTPClient = defaultHTTPClient()
	}
	return &CodexAdapter{config: config}
}
func (a *CodexAdapter) Service() string { return oauth.OAuthServiceCodex }
func (a *CodexAdapter) BuildAuthorizationURL(_ context.Context, in oauth.AuthorizationInput) (oauth.AuthorizationResult, error) {
	v := url.Values{"response_type": {"code"}, "client_id": {a.config.ClientID}, "redirect_uri": {in.RedirectURI}, "scope": {strings.Join(a.config.Scopes, " ")}, "code_challenge": {challenge(in.CodeVerifier)}, "code_challenge_method": {"S256"}, "state": {in.State}, "id_token_add_organizations": {"true"}, "codex_cli_simplified_flow": {"true"}}
	return oauth.AuthorizationResult{AuthorizationURL: a.config.AuthorizeURL + "?" + v.Encode(), ExpiresAt: nowPlus(10)}, nil
}
func (a *CodexAdapter) Exchange(ctx context.Context, code, state, verifier, redirect string, client *http.Client) (*oauth.OAuthCredential, error) {
	return a.token(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "client_id": {a.config.ClientID}, "code_verifier": {verifier}}, client)
}
func (a *CodexAdapter) Refresh(ctx context.Context, old *oauth.OAuthCredential, client *http.Client) (*oauth.OAuthCredential, error) {
	return a.token(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {old.RefreshToken}, "client_id": {a.config.ClientID}}, client)
}
func (a *CodexAdapter) token(ctx context.Context, values url.Values, client *http.Client) (*oauth.OAuthCredential, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.config.TokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "codex-tui/0.146.0")
	req.Header.Set("originator", "codex-tui")
	var token tokenResponse
	operation := "exchange_code"
	if values.Get("grant_type") == "refresh_token" {
		operation = "refresh_token"
	}
	if _, err := readResponseDo(clientOrDefault(clientOr(a.config.HTTPClient, client)), req, HTTPCallMeta{Provider: a.Service(), Operation: operation}, &token); err != nil {
		return nil, err
	}
	result, err := credential(token)
	if err != nil {
		return nil, err
	}
	claims := decodeClaims(token.IDToken)
	if result.Email == "" {
		result.Email = claimString(claims, "email")
	}
	auth, _ := claims["https://api.openai.com/auth"].(map[string]any)
	result.AccountID = claimString(claims, "chatgpt_account_id")
	if result.AccountID == "" {
		result.AccountID = claimString(auth, "chatgpt_account_id")
	}
	if result.AccountID == "" {
		result.AccountID = claimString(claims, "organization_id")
	}
	return result, nil
}
