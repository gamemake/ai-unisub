package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
	"github.com/ai-unisub/ai-unisub/internal/repository"
	"github.com/gin-gonic/gin"
)

const (
	grokDeviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"
	maxPendingGrokOAuth = 20
	maxOAuthBodyBytes   = 1 << 20
	maxDeviceCodeTTL    = 24 * time.Hour
	maxOAuthTokenTTL    = 365 * 24 * time.Hour
)

type grokOAuthFlow struct {
	ID               string
	Owner            string
	DeviceCode       string
	VerificationURI  string
	VerificationFull string
	UserCode         string
	ExpiresAt        time.Time
	NextPollAt       time.Time
	PollInterval     time.Duration
	Account          oauthAccountRequest
	Client           *http.Client
	Polling          bool
	Finalizing       bool
	Token            *grokOAuthTokenResponse
}

type grokDeviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int64  `json:"expires_in"`
	Interval                int64  `json:"interval"`
}

type grokOAuthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    int64  `json:"expires_in"`
}

type grokOAuthErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func (s *Server) startGrokOAuthDevice(c *gin.Context) {
	username, _ := c.Get("admin_username")
	owner, _ := username.(string)
	if !s.allowRate("grok-oauth-start:"+owner, 10, time.Minute) {
		apiError(c, http.StatusTooManyRequests, "rate_limited", "too many Grok OAuth login attempts")
		return
	}

	account, client, ok := s.bindOAuthAccountRequest(c)
	if !ok {
		return
	}

	values := url.Values{
		"client_id": {s.cfg.GrokOAuth.ClientID},
		"scope":     {strings.Join(s.cfg.GrokOAuth.Scopes, " ")},
		"referrer":  {"ai-unisub"},
	}
	var device grokDeviceCodeResponse
	status, oauthErr, err := s.doGrokOAuthForm(c.Request.Context(), client, "/oauth2/device/code", values, &device)
	if err != nil {
		closeHTTPClient(client)
		apiError(c, http.StatusBadGateway, "oauth_unavailable", "could not contact the xAI authorization server")
		return
	}
	if status < 200 || status >= 300 {
		closeHTTPClient(client)
		apiError(c, http.StatusBadGateway, "oauth_rejected", oauthErrorMessage(oauthErr, "xAI rejected the device authorization request"))
		return
	}
	if device.DeviceCode == "" || device.UserCode == "" || device.VerificationURI == "" || device.ExpiresIn <= 0 ||
		device.ExpiresIn > int64(maxDeviceCodeTTL/time.Second) {
		closeHTTPClient(client)
		apiError(c, http.StatusBadGateway, "oauth_invalid_response", "xAI returned an incomplete device authorization response")
		return
	}
	if !validUserCode(device.UserCode) || !s.validGrokVerificationURI(device.VerificationURI) ||
		(device.VerificationURIComplete != "" && !s.validGrokVerificationURI(device.VerificationURIComplete)) {
		closeHTTPClient(client)
		apiError(c, http.StatusBadGateway, "oauth_invalid_response", "xAI returned an invalid verification URL or user code")
		return
	}

	flowID, err := randomFlowID()
	if err != nil {
		closeHTTPClient(client)
		apiError(c, http.StatusInternalServerError, "internal_error", "could not create OAuth login state")
		return
	}
	interval := time.Duration(device.Interval) * time.Second
	if interval < time.Second {
		interval = 5 * time.Second
	}
	if interval > 30*time.Second {
		interval = 30 * time.Second
	}
	now := time.Now()
	flow := &grokOAuthFlow{
		ID: flowID, Owner: owner, DeviceCode: device.DeviceCode, VerificationURI: device.VerificationURI,
		VerificationFull: device.VerificationURIComplete, UserCode: device.UserCode,
		ExpiresAt: now.Add(time.Duration(device.ExpiresIn) * time.Second), NextPollAt: now.Add(interval),
		PollInterval: interval, Account: account, Client: client,
	}

	s.grokOAuthMu.Lock()
	s.cleanupGrokOAuthFlowsLocked(now)
	if len(s.grokOAuthFlows) >= maxPendingGrokOAuth {
		s.grokOAuthMu.Unlock()
		closeHTTPClient(client)
		apiError(c, http.StatusTooManyRequests, "oauth_capacity", "too many pending Grok OAuth logins")
		return
	}
	s.grokOAuthFlows[flowID] = flow
	s.grokOAuthMu.Unlock()

	verificationFull := device.VerificationURIComplete
	if verificationFull == "" {
		separator := "?"
		if strings.Contains(device.VerificationURI, "?") {
			separator = "&"
		}
		verificationFull = device.VerificationURI + separator + "user_code=" + url.QueryEscape(device.UserCode)
	}
	c.JSON(http.StatusCreated, gin.H{
		"flow_id": flowID, "status": "pending", "user_code": device.UserCode,
		"verification_uri": device.VerificationURI, "verification_uri_complete": verificationFull,
		"expires_at": flow.ExpiresAt.UTC(), "interval": int64(interval / time.Second),
	})
}

func (s *Server) pollGrokOAuthDevice(c *gin.Context) {
	var request struct {
		FlowID string `json:"flow_id"`
	}
	if c.ShouldBindJSON(&request) != nil || request.FlowID == "" {
		apiError(c, http.StatusBadRequest, "invalid_request", "flow_id is required")
		return
	}
	username, _ := c.Get("admin_username")
	owner, _ := username.(string)

	now := time.Now()
	s.grokOAuthMu.Lock()
	s.cleanupGrokOAuthFlowsLocked(now)
	flow, ok := s.grokOAuthFlows[request.FlowID]
	if !ok || flow.Owner != owner {
		s.grokOAuthMu.Unlock()
		apiError(c, http.StatusNotFound, "oauth_flow_not_found", "Grok OAuth login was not found or has expired")
		return
	}
	if flow.Token != nil {
		s.grokOAuthMu.Unlock()
		s.finishGrokOAuthFlow(c, flow)
		return
	}
	if flow.Polling || now.Before(flow.NextPollAt) {
		retry := time.Until(flow.NextPollAt)
		if retry < time.Second {
			retry = time.Second
		}
		s.grokOAuthMu.Unlock()
		c.JSON(http.StatusAccepted, gin.H{"status": "pending", "retry_after": int64((retry + time.Second - 1) / time.Second)})
		return
	}
	flow.Polling = true
	flow.NextPollAt = now.Add(flow.PollInterval)
	s.grokOAuthMu.Unlock()

	values := url.Values{
		"grant_type":  {grokDeviceGrantType},
		"device_code": {flow.DeviceCode},
		"client_id":   {s.cfg.GrokOAuth.ClientID},
	}
	var token grokOAuthTokenResponse
	status, oauthErr, err := s.doGrokOAuthForm(c.Request.Context(), flow.Client, "/oauth2/token", values, &token)

	s.grokOAuthMu.Lock()
	current, stillPending := s.grokOAuthFlows[flow.ID]
	if stillPending {
		current.Polling = false
	}
	if err == nil && status >= 200 && status < 300 && token.AccessToken != "" && stillPending {
		current.Token = &token
	}
	s.grokOAuthMu.Unlock()

	if err != nil {
		apiError(c, http.StatusBadGateway, "oauth_unavailable", "could not contact the xAI token endpoint")
		return
	}
	if status >= 200 && status < 300 {
		if token.AccessToken == "" {
			apiError(c, http.StatusBadGateway, "oauth_invalid_response", "xAI returned a token response without an access token")
			return
		}
		s.finishGrokOAuthFlow(c, flow)
		return
	}

	switch oauthErr.Error {
	case "authorization_pending":
		c.JSON(http.StatusAccepted, gin.H{"status": "pending", "retry_after": int64(flow.PollInterval / time.Second)})
	case "slow_down":
		s.grokOAuthMu.Lock()
		if current, ok := s.grokOAuthFlows[flow.ID]; ok {
			current.PollInterval += 5 * time.Second
			if current.PollInterval > 30*time.Second {
				current.PollInterval = 30 * time.Second
			}
			current.NextPollAt = time.Now().Add(current.PollInterval)
		}
		s.grokOAuthMu.Unlock()
		c.JSON(http.StatusAccepted, gin.H{"status": "pending", "retry_after": int64(flow.PollInterval / time.Second)})
	case "access_denied":
		s.removeGrokOAuthFlow(flow.ID)
		apiError(c, http.StatusForbidden, "oauth_access_denied", "Grok authorization was denied")
	case "expired_token":
		s.removeGrokOAuthFlow(flow.ID)
		apiError(c, http.StatusGone, "oauth_expired", "Grok device authorization expired; start a new login")
	default:
		apiError(c, http.StatusBadGateway, "oauth_rejected", oauthErrorMessage(oauthErr, "xAI rejected the token request"))
	}
}

func (s *Server) finishGrokOAuthFlow(c *gin.Context, flow *grokOAuthFlow) {
	s.grokOAuthMu.Lock()
	current, ok := s.grokOAuthFlows[flow.ID]
	if !ok || current.Token == nil {
		s.grokOAuthMu.Unlock()
		apiError(c, http.StatusConflict, "oauth_not_ready", "Grok authorization is not complete")
		return
	}
	if current.Finalizing {
		s.grokOAuthMu.Unlock()
		c.JSON(http.StatusAccepted, gin.H{"status": "finalizing", "retry_after": 1})
		return
	}
	current.Finalizing = true
	token := *current.Token
	accountRequest := current.Account
	s.grokOAuthMu.Unlock()

	credentials := model.Credentials{
		AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, IDToken: token.IDToken,
		TokenType: firstNonEmpty(token.TokenType, "Bearer"), ClientID: s.cfg.GrokOAuth.ClientID, Scope: token.Scope,
	}
	expiresAt := oauthTokenExpiry(token.ExpiresIn)
	userID := userFromContext(c).ID
	account, err := s.repo.CreateAccount(c.Request.Context(), repository.CreateAccountParams{
		Name: accountRequest.Name, Provider: model.ProviderGrok, AuthType: "oauth", Credentials: credentials,
		Metadata: accountRequest.Metadata, ConcurrencyLimit: accountRequest.ConcurrencyLimit,
		ConcurrencyQueueTimeoutSeconds: accountRequest.ConcurrencyQueueTimeoutSeconds,
		ProxyURL:                       accountRequest.ProxyURL, TokenExpiresAt: expiresAt, CreatedByUserID: &userID,
	})
	if err != nil {
		s.grokOAuthMu.Lock()
		if current, ok := s.grokOAuthFlows[flow.ID]; ok {
			current.Finalizing = false
		}
		s.grokOAuthMu.Unlock()
		apiError(c, http.StatusInternalServerError, "internal_error", "authorization succeeded but the Grok account could not be created; retry polling")
		return
	}
	if s.cfg.Providers.GrokBilling != "" {
		if refreshed, refreshErr := s.refreshGrokQuota(c.Request.Context(), account); refreshErr == nil {
			account = refreshed
		}
	}
	s.removeGrokOAuthFlow(flow.ID)
	c.JSON(http.StatusCreated, gin.H{"status": "complete", "account": account})
}

func (s *Server) grokCredentialsForRequest(ctx context.Context, account model.Account, credentials model.Credentials, force bool) (model.Credentials, error) {
	if account.Provider != model.ProviderGrok || account.AuthType != "oauth" {
		return credentials, nil
	}
	originalAccessToken := credentials.AccessToken
	if !force && (account.TokenExpiresAt == nil || time.Until(*account.TokenExpiresAt) > oauthRefreshSkew(account.Provider)) {
		return credentials, nil
	}
	lockValue, _ := s.grokRefreshLocks.LoadOrStore(account.ID, new(sync.Mutex))
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
		return model.Credentials{}, errors.New("Grok OAuth token expired and no refresh token is available")
	}
	client, err := s.clientForAccount(latest)
	if err != nil {
		return model.Credentials{}, err
	}
	clientID := firstNonEmpty(credentials.ClientID, s.cfg.GrokOAuth.ClientID)
	values := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {credentials.RefreshToken},
		"client_id":     {clientID},
	}
	var token grokOAuthTokenResponse
	status, oauthErr, err := s.doGrokOAuthForm(ctx, client, "/oauth2/token", values, &token)
	if err != nil {
		return model.Credentials{}, errors.New("could not contact xAI to refresh the Grok OAuth token")
	}
	if status < 200 || status >= 300 || token.AccessToken == "" {
		return model.Credentials{}, fmt.Errorf("Grok OAuth refresh failed: %s", oauthErrorMessage(oauthErr, "token refresh rejected"))
	}
	credentials.AccessToken = token.AccessToken
	credentials.TokenType = firstNonEmpty(token.TokenType, credentials.TokenType, "Bearer")
	credentials.ClientID = clientID
	if token.RefreshToken != "" {
		credentials.RefreshToken = token.RefreshToken
	}
	if token.IDToken != "" {
		credentials.IDToken = token.IDToken
	}
	if token.Scope != "" {
		credentials.Scope = token.Scope
	}
	expiresAt := oauthTokenExpiry(token.ExpiresIn)
	if err := s.repo.UpdateAccountCredentials(ctx, account.ID, credentials, expiresAt); err != nil {
		return model.Credentials{}, err
	}
	return credentials, nil
}

func (s *Server) doGrokOAuthForm(ctx context.Context, client *http.Client, path string, values url.Values, output any) (int, grokOAuthErrorResponse, error) {
	endpoint := strings.TrimRight(s.cfg.GrokOAuth.Issuer, "/") + path
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return 0, grokOAuthErrorResponse{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "grok-shell/"+s.cfg.GrokOAuth.ClientVersion+" ai-unisub")
	request.Header.Set("x-grok-client-version", s.cfg.GrokOAuth.ClientVersion)
	request.Header.Set("x-grok-client-surface", "ui")
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
	var oauthErr grokOAuthErrorResponse
	_ = json.Unmarshal(body, &oauthErr)
	return response.StatusCode, oauthErr, nil
}

func (s *Server) validGrokVerificationURI(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil {
		return false
	}
	if s.cfg.AllowTestUpstreams {
		return parsed.Scheme == "http" || parsed.Scheme == "https"
	}
	if parsed.Scheme != "https" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "accounts.x.ai" || host == "auth.x.ai"
}

func (s *Server) cleanupGrokOAuthFlowsLocked(now time.Time) {
	for id, flow := range s.grokOAuthFlows {
		if !now.Before(flow.ExpiresAt) {
			delete(s.grokOAuthFlows, id)
			closeHTTPClient(flow.Client)
		}
	}
}

func (s *Server) removeGrokOAuthFlow(id string) {
	s.grokOAuthMu.Lock()
	flow := s.grokOAuthFlows[id]
	delete(s.grokOAuthFlows, id)
	s.grokOAuthMu.Unlock()
	if flow != nil {
		closeHTTPClient(flow.Client)
	}
}

func closeHTTPClient(client *http.Client) {
	if client == nil {
		return
	}
	if transport, ok := client.Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
}

func randomFlowID() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func validUserCode(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if !(character >= 'A' && character <= 'Z') && !(character >= 'a' && character <= 'z') &&
			!(character >= '0' && character <= '9') && character != '-' {
			return false
		}
	}
	return true
}

func oauthErrorMessage(response grokOAuthErrorResponse, fallback string) string {
	message := fallback
	if response.ErrorDescription != "" {
		message = response.ErrorDescription
	} else if response.Error != "" {
		message = response.Error
	}
	message = strings.Map(func(character rune) rune {
		if character < 0x20 || character == 0x7f {
			return -1
		}
		return character
	}, message)
	runes := []rune(message)
	if len(runes) > 512 {
		message = string(runes[:512])
	}
	return message
}

func oauthTokenExpiry(expiresIn int64) *time.Time {
	if expiresIn <= 0 {
		return nil
	}
	maximum := int64(maxOAuthTokenTTL / time.Second)
	if expiresIn > maximum {
		expiresIn = maximum
	}
	expires := time.Now().Add(time.Duration(expiresIn) * time.Second).UTC()
	return &expires
}
