package oauth

import (
	"cmp"
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

type OpenAIConfig struct {
	AuthorizeURL string
	TokenURL     string
	ClientID     string
	Scopes       []string
	HTTPClient   *http.Client
}

type OpenAIAdapter struct {
	config OpenAIConfig
}

func NewOpenAI(config OpenAIConfig) *OpenAIAdapter {
	config.AuthorizeURL = cmp.Or(config.AuthorizeURL, "https://auth.openai.com/oauth/authorize")
	config.TokenURL = cmp.Or(config.TokenURL, "https://auth.openai.com/oauth/token")
	config.ClientID = cmp.Or(config.ClientID, "app_EMoamEEZ73f0CkXaXp7hrann")
	if len(config.Scopes) == 0 {
		config.Scopes = []string{"openid", "profile", "email", "offline_access"}
	} else {
		config.Scopes = slices.Clone(config.Scopes)
	}
	return &OpenAIAdapter{config: config}
}

func (a *OpenAIAdapter) Service() string { return OAuthServiceOpenAI }

func (a *OpenAIAdapter) BuildAuthorizationURL(_ context.Context, input AuthorizationInput) (AuthorizationResult, error) {
	values := url.Values{
		"response_type":              {"code"},
		"client_id":                  {a.config.ClientID},
		"redirect_uri":               {input.RedirectURI},
		"scope":                      {strings.Join(a.config.Scopes, " ")},
		"code_challenge":             {pkceChallenge(input.CodeVerifier)},
		"code_challenge_method":      {"S256"},
		"state":                      {input.State},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
	}
	return AuthorizationResult{AuthorizationURL: a.config.AuthorizeURL + "?" + values.Encode(), ExpiresAt: time.Now().Add(oauthSessionTTL)}, nil
}

func (a *OpenAIAdapter) Exchange(ctx context.Context, code, _, verifier, redirectURI string, client *http.Client) (*OAuthCredential, error) {
	return a.token(ctx, url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI},
		"client_id": {a.config.ClientID}, "code_verifier": {verifier},
	}, client)
}

func (a *OpenAIAdapter) Refresh(ctx context.Context, credential *OAuthCredential, client *http.Client) (*OAuthCredential, error) {
	if credential == nil || credential.RefreshToken == "" {
		return nil, errCredentialNoRefreshToken
	}
	return a.token(ctx, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {credential.RefreshToken}, "client_id": {a.config.ClientID},
	}, client)
}

func (a *OpenAIAdapter) token(ctx context.Context, values url.Values, client *http.Client) (*OAuthCredential, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.config.TokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "codex-tui/0.146.0")
	request.Header.Set("originator", "codex-tui")
	var token tokenResponse
	if err := doOAuthRequest(selectHTTPClient(a.config.HTTPClient, client), request, &token); err != nil {
		return nil, err
	}
	credential, err := credentialFromToken(token)
	if err != nil {
		return nil, err
	}
	claims := decodeJWTClaims(token.IDToken)
	credential.Email = claimString(claims, "email")
	auth, _ := claims["https://api.openai.com/auth"].(map[string]any)
	credential.AccountID = cmp.Or(
		claimString(claims, "chatgpt_account_id"),
		claimString(auth, "chatgpt_account_id"),
		claimString(claims, "organization_id"),
	)
	return credential, nil
}
