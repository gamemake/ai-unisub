package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
	"github.com/ai-unisub/ai-unisub/internal/repository"
)

type claudeOAuthTokenRequest struct {
	GrantType    string `json:"grant_type"`
	Code         string `json:"code,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	RedirectURI  string `json:"redirect_uri,omitempty"`
	ClientID     string `json:"client_id"`
	CodeVerifier string `json:"code_verifier,omitempty"`
	State        string `json:"state,omitempty"`
}

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    int64  `json:"expires_in"`
}

func (s *Server) exchangePKCECode(ctx context.Context, flow *pkceOAuthFlow, code string) (model.Credentials, *time.Time, grokOAuthErrorResponse, error) {
	switch flow.Provider {
	case model.ProviderClaude:
		return s.exchangeClaudeCode(ctx, flow, code)
	case model.ProviderCodex:
		return s.exchangeCodexCode(ctx, flow, code)
	default:
		return model.Credentials{}, nil, grokOAuthErrorResponse{}, errors.New("unsupported OAuth provider")
	}
}

func (s *Server) exchangeClaudeCode(ctx context.Context, flow *pkceOAuthFlow, code string) (model.Credentials, *time.Time, grokOAuthErrorResponse, error) {
	payload := claudeOAuthTokenRequest{
		GrantType: "authorization_code", Code: code, RedirectURI: s.cfg.ClaudeOAuth.RedirectURI,
		ClientID: s.cfg.ClaudeOAuth.ClientID, CodeVerifier: flow.CodeVerifier, State: flow.State,
	}
	var token oauthTokenResponse
	status, oauthErr, err := s.doClaudeOAuthJSON(ctx, flow.Client, payload, &token)
	if err != nil {
		return model.Credentials{}, nil, oauthErr, errors.New("could not contact the Claude token endpoint")
	}
	if status < 200 || status >= 300 {
		return model.Credentials{}, nil, oauthErr, fmt.Errorf("%s", oauthErrorMessage(oauthErr, "Claude rejected the token request"))
	}
	credentials := model.Credentials{
		AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, TokenType: firstNonEmpty(token.TokenType, "Bearer"),
		ClientID: s.cfg.ClaudeOAuth.ClientID, Scope: firstNonEmpty(token.Scope, strings.Join(s.cfg.ClaudeOAuth.Scopes, " ")),
	}
	return credentials, tokenExpiryFromResponse(token.ExpiresIn, token.AccessToken), grokOAuthErrorResponse{}, nil
}

func (s *Server) claudeCredentialsForRequest(ctx context.Context, account model.Account, credentials model.Credentials, force bool) (model.Credentials, error) {
	if account.Provider != model.ProviderClaude || account.AuthType != "oauth" {
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
		return model.Credentials{}, errors.New("Claude OAuth token expired and no refresh token is available")
	}
	client, err := s.clientForAccount(latest)
	if err != nil {
		return model.Credentials{}, err
	}
	clientID := firstNonEmpty(credentials.ClientID, s.cfg.ClaudeOAuth.ClientID)
	var token oauthTokenResponse
	status, oauthErr, err := s.doClaudeOAuthJSON(ctx, client, claudeOAuthTokenRequest{
		GrantType: "refresh_token", RefreshToken: credentials.RefreshToken, ClientID: clientID,
	}, &token)
	if err != nil {
		return model.Credentials{}, errors.New("could not contact Anthropic to refresh the Claude OAuth token")
	}
	if status < 200 || status >= 300 || token.AccessToken == "" {
		return model.Credentials{}, fmt.Errorf("Claude OAuth refresh failed: %s", oauthErrorMessage(oauthErr, "token refresh rejected"))
	}
	credentials.AccessToken = token.AccessToken
	credentials.TokenType = firstNonEmpty(token.TokenType, credentials.TokenType, "Bearer")
	credentials.ClientID = clientID
	if token.RefreshToken != "" {
		credentials.RefreshToken = token.RefreshToken
	}
	if token.Scope != "" {
		credentials.Scope = token.Scope
	}
	expiresAt := tokenExpiryFromResponse(token.ExpiresIn, token.AccessToken)
	if err := s.repo.UpdateAccountCredentials(ctx, account.ID, credentials, expiresAt); err != nil {
		return model.Credentials{}, err
	}
	return credentials, nil
}

func (s *Server) doClaudeOAuthJSON(ctx context.Context, client *http.Client, payload claudeOAuthTokenRequest, output any) (int, grokOAuthErrorResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, grokOAuthErrorResponse{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.ClaudeOAuth.TokenURL, bytes.NewReader(body))
	if err != nil {
		return 0, grokOAuthErrorResponse{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", defaultClaudeCLIUserAgent)
	return readOAuthResponse(client, request, output)
}

func readOAuthResponse(client *http.Client, request *http.Request, output any) (int, grokOAuthErrorResponse, error) {
	response, err := doHTTP(client, request)
	if err != nil {
		return 0, grokOAuthErrorResponse{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxOAuthBodyBytes+1))
	if err != nil {
		return response.StatusCode, grokOAuthErrorResponse{}, err
	}
	if len(body) > maxOAuthBodyBytes {
		return response.StatusCode, grokOAuthErrorResponse{}, errors.New("OAuth response is too large")
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		if err := json.Unmarshal(body, output); err != nil {
			return response.StatusCode, grokOAuthErrorResponse{}, err
		}
		return response.StatusCode, grokOAuthErrorResponse{}, nil
	}
	return response.StatusCode, parseOAuthErrorBody(body), nil
}

func parseOAuthErrorBody(body []byte) grokOAuthErrorResponse {
	var oauthErr grokOAuthErrorResponse
	if err := json.Unmarshal(body, &oauthErr); err == nil && (oauthErr.Error != "" || oauthErr.ErrorDescription != "") {
		return oauthErr
	}
	var nested struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &nested); err == nil && nested.Error.Message != "" {
		return grokOAuthErrorResponse{
			Error:            firstNonEmpty(nested.Error.Type, nested.Type, "oauth_error"),
			ErrorDescription: nested.Error.Message,
		}
	}
	return oauthErr
}

func createOAuthAccountParams(account oauthAccountRequest, provider model.Provider, credentials model.Credentials, expiresAt *time.Time, userID int64) repository.CreateAccountParams {
	return repository.CreateAccountParams{
		Name: account.Name, Provider: provider, AuthType: "oauth", Credentials: credentials,
		Metadata: account.Metadata, ConcurrencyLimit: account.ConcurrencyLimit,
		ConcurrencyQueueTimeoutSeconds: account.ConcurrencyQueueTimeoutSeconds,
		ProxyURL:                       account.ProxyURL, TokenExpiresAt: expiresAt, CreatedByUserID: &userID,
	}
}

func tokenExpiryFromResponse(expiresIn int64, accessToken string) *time.Time {
	if exp := oauthTokenExpiry(expiresIn); exp != nil {
		return exp
	}
	claims, err := decodeJWTClaims(accessToken)
	if err != nil {
		return nil
	}
	exp, ok := claims["exp"].(float64)
	if !ok || exp <= 0 {
		return nil
	}
	expires := time.Unix(int64(exp), 0).UTC()
	if expires.Before(time.Now()) {
		return nil
	}
	maximum := time.Now().Add(maxOAuthTokenTTL)
	if expires.After(maximum) {
		expires = maximum
	}
	return &expires
}
