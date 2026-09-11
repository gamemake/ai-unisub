package adapters

import (
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
	if config.AuthorizeURL == "" {
		config.AuthorizeURL = "https://auth.openai.com/oauth/authorize"
	}
	if config.TokenURL == "" {
		config.TokenURL = "https://auth.openai.com/oauth/token"
	}
	if config.ClientID == "" {
		config.ClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	}
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
func (a *CodexAdapter) Exchange(ctx context.Context, code, state, verifier, redirect string) (*oauth.OAuthCredential, error) {
	return a.token(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "client_id": {a.config.ClientID}, "code_verifier": {verifier}})
}
func (a *CodexAdapter) Refresh(ctx context.Context, old *oauth.OAuthCredential) (*oauth.OAuthCredential, error) {
	return a.token(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {old.RefreshToken}, "client_id": {a.config.ClientID}})
}
func (a *CodexAdapter) token(ctx context.Context, values url.Values) (*oauth.OAuthCredential, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.config.TokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "codex-tui/0.146.0")
	req.Header.Set("originator", "codex-tui")
	var token tokenResponse
	if _, err := readResponseDo(clientWithProxy(ctx, a.config.HTTPClient), req, &token); err != nil {
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
