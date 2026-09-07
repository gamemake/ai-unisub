package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
)

func (s *Server) exchangeCodexCode(ctx context.Context, flow *pkceOAuthFlow, code string) (model.Credentials, *time.Time, grokOAuthErrorResponse, error) {
	values := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {s.cfg.CodexOAuth.RedirectURI},
		"client_id":     {s.cfg.CodexOAuth.ClientID},
		"code_verifier": {flow.CodeVerifier},
	}
	var token oauthTokenResponse
	status, oauthErr, err := s.doCodexOAuthForm(ctx, flow.Client, values, &token)
	if err != nil {
		return model.Credentials{}, nil, oauthErr, errors.New("could not contact the Codex token endpoint")
	}
	if status < 200 || status >= 300 {
		return model.Credentials{}, nil, oauthErr, fmt.Errorf("%s", oauthErrorMessage(oauthErr, "Codex rejected the token request"))
	}
	credentials := credentialsFromCodexToken(token, s.cfg.CodexOAuth.ClientID, strings.Join(s.cfg.CodexOAuth.Scopes, " "))
	return credentials, tokenExpiryFromResponse(token.ExpiresIn, token.AccessToken), grokOAuthErrorResponse{}, nil
}

func (s *Server) codexCredentialsForRequest(ctx context.Context, account model.Account, credentials model.Credentials, force bool) (model.Credentials, error) {
	if account.Provider != model.ProviderCodex || account.AuthType != "oauth" {
		return credentials, nil
	}
	originalAccessToken := credentials.AccessToken
	if !force && (account.TokenExpiresAt == nil || time.Until(*account.TokenExpiresAt) > oauthRefreshSkew(account.Provider)) {
		return credentials, nil
	}
	lockValue, _ := s.oauthRefreshLocks.LoadOrStore(account.ID, new(sync.Mutex))
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	latest, err := s.repo.GetAccount(ctx, account.ID)
	if err != nil {
		return model.Credentials{}, err
	}
	credentials, err = s.repo.Credentials(ctx, latest)
	if err != nil {
		return model.Credentials{}, err
	}
	if force && credentials.AccessToken != originalAccessToken {
		return credentials, nil
	}
	if !force && (latest.TokenExpiresAt == nil || time.Until(*latest.TokenExpiresAt) > oauthRefreshSkew(latest.Provider)) {
		return credentials, nil
	}
	if credentials.RefreshToken == "" {
		return model.Credentials{}, errors.New("Codex OAuth token expired and no refresh token is available")
	}
	client, err := s.clientForAccount(latest)
	if err != nil {
		return model.Credentials{}, err
	}
	clientID := firstNonEmpty(credentials.ClientID, s.cfg.CodexOAuth.ClientID)
	values := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {credentials.RefreshToken},
		"client_id":     {clientID},
	}
	var token oauthTokenResponse
	status, oauthErr, err := s.doCodexOAuthForm(ctx, client, values, &token)
	if err != nil {
		return model.Credentials{}, errors.New("could not contact OpenAI to refresh the Codex OAuth token")
	}
	if status < 200 || status >= 300 || token.AccessToken == "" {
		return model.Credentials{}, fmt.Errorf("Codex OAuth refresh failed: %s", oauthErrorMessage(oauthErr, "token refresh rejected"))
	}
	refreshed := credentialsFromCodexToken(token, clientID, credentials.Scope)
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = credentials.RefreshToken
	}
	if refreshed.ChatGPTAccountID == "" {
		refreshed.ChatGPTAccountID = credentials.ChatGPTAccountID
	}
	if refreshed.OrganizationID == "" {
		refreshed.OrganizationID = credentials.OrganizationID
	}
	if refreshed.UserID == "" {
		refreshed.UserID = credentials.UserID
	}
	if refreshed.Email == "" {
		refreshed.Email = credentials.Email
	}
	expiresAt := tokenExpiryFromResponse(token.ExpiresIn, token.AccessToken)
	if err := s.repo.UpdateAccountCredentials(ctx, account.ID, refreshed, expiresAt); err != nil {
		return model.Credentials{}, err
	}
	return refreshed, nil
}

func (s *Server) doCodexOAuthForm(ctx context.Context, client *http.Client, values url.Values, output any) (int, grokOAuthErrorResponse, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.CodexOAuth.TokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return 0, grokOAuthErrorResponse{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", codexCLIUserAgent())
	request.Header.Set("originator", codexOriginator)
	return readOAuthResponse(client, request, output)
}

func credentialsFromCodexToken(token oauthTokenResponse, clientID, fallbackScope string) model.Credentials {
	credentials := model.Credentials{
		AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, IDToken: token.IDToken,
		TokenType: firstNonEmpty(token.TokenType, "Bearer"), ClientID: clientID,
		Scope: firstNonEmpty(token.Scope, fallbackScope),
	}
	applyCodexClaims(&credentials, token.IDToken)
	if credentials.ChatGPTAccountID == "" {
		applyCodexClaims(&credentials, token.AccessToken)
	}
	return credentials
}

func applyCodexClaims(credentials *model.Credentials, token string) {
	claims, err := decodeJWTClaims(token)
	if err != nil {
		return
	}
	if credentials.Email == "" {
		credentials.Email, _ = claims["email"].(string)
	}
	auth, _ := claims["https://api.openai.com/auth"].(map[string]any)
	if credentials.ChatGPTAccountID == "" {
		credentials.ChatGPTAccountID = firstNonEmpty(stringClaim(claims, "chatgpt_account_id"), stringClaim(auth, "chatgpt_account_id"))
	}
	if credentials.UserID == "" {
		credentials.UserID = firstNonEmpty(stringClaim(claims, "chatgpt_user_id"), stringClaim(auth, "chatgpt_user_id"), stringClaim(claims, "sub"))
	}
	if credentials.OrganizationID == "" {
		credentials.OrganizationID = firstNonEmpty(stringClaim(claims, "organization_id"), stringClaim(auth, "organization_id"))
	}
}

func stringClaim(claims map[string]any, key string) string {
	if claims == nil {
		return ""
	}
	value, _ := claims[key].(string)
	return value
}

func decodeJWTClaims(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil, errors.New("token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		padded := parts[1]
		if pad := len(padded) % 4; pad != 0 {
			padded += strings.Repeat("=", 4-pad)
		}
		payload, err = base64.URLEncoding.DecodeString(padded)
		if err != nil {
			return nil, err
		}
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	return claims, nil
}
