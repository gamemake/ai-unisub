package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
	"github.com/gin-gonic/gin"
)

const (
	maxPendingPKCEOAuth = 20
	pkceFlowTTL         = 10 * time.Minute
	maxAuthCodeBytes    = 4096
)

type pkceOAuthFlow struct {
	ID           string
	Owner        string
	Provider     model.Provider
	State        string
	CodeVerifier string
	ExpiresAt    time.Time
	Account      oauthAccountRequest
	Client       *http.Client
	Finalizing   bool
}

func (s *Server) startClaudeOAuth(c *gin.Context) {
	s.startPKCEOAuth(c, model.ProviderClaude)
}

func (s *Server) exchangeClaudeOAuth(c *gin.Context) {
	s.exchangePKCEOAuth(c, model.ProviderClaude)
}

func (s *Server) startCodexOAuth(c *gin.Context) {
	s.startPKCEOAuth(c, model.ProviderCodex)
}

func (s *Server) exchangeCodexOAuth(c *gin.Context) {
	s.exchangePKCEOAuth(c, model.ProviderCodex)
}

func (s *Server) startPKCEOAuth(c *gin.Context, provider model.Provider) {
	username, _ := c.Get("admin_username")
	owner, _ := username.(string)
	if !s.allowRate(string(provider)+"-oauth-start:"+owner, 10, time.Minute) {
		apiError(c, http.StatusTooManyRequests, "rate_limited", "too many "+providerDisplayName(provider)+" OAuth login attempts")
		return
	}
	account, client, ok := s.bindOAuthAccountRequest(c)
	if !ok {
		return
	}

	flowID, err := randomFlowID()
	if err != nil {
		closeHTTPClient(client)
		apiError(c, http.StatusInternalServerError, "internal_error", "could not create OAuth login state")
		return
	}
	state, err := randomFlowID()
	if err != nil {
		closeHTTPClient(client)
		apiError(c, http.StatusInternalServerError, "internal_error", "could not create OAuth login state")
		return
	}
	verifier, challenge, err := generatePKCE()
	if err != nil {
		closeHTTPClient(client)
		apiError(c, http.StatusInternalServerError, "internal_error", "could not create OAuth login state")
		return
	}
	authorizationURL, err := s.pkceAuthorizationURL(provider, state, challenge)
	if err != nil {
		closeHTTPClient(client)
		apiError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	now := time.Now()
	flow := &pkceOAuthFlow{
		ID: flowID, Owner: owner, Provider: provider, State: state, CodeVerifier: verifier,
		ExpiresAt: now.Add(pkceFlowTTL), Account: account, Client: client,
	}

	s.pkceOAuthMu.Lock()
	s.cleanupPKCEOAuthFlowsLocked(now)
	if len(s.pkceOAuthFlows) >= maxPendingPKCEOAuth {
		s.pkceOAuthMu.Unlock()
		closeHTTPClient(client)
		apiError(c, http.StatusTooManyRequests, "oauth_capacity", "too many pending OAuth logins")
		return
	}
	s.pkceOAuthFlows[flowID] = flow
	s.pkceOAuthMu.Unlock()

	c.JSON(http.StatusCreated, gin.H{
		"flow_id": flowID, "status": "pending", "authorization_url": authorizationURL,
		"expires_at": flow.ExpiresAt.UTC(),
	})
}

func (s *Server) exchangePKCEOAuth(c *gin.Context, provider model.Provider) {
	var request struct {
		FlowID string `json:"flow_id"`
		Code   string `json:"code"`
	}
	if c.ShouldBindJSON(&request) != nil || request.FlowID == "" {
		apiError(c, http.StatusBadRequest, "invalid_request", "flow_id is required")
		return
	}
	code, returnedState, err := parseAuthorizationInput(request.Code)
	if err != nil {
		apiError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	username, _ := c.Get("admin_username")
	owner, _ := username.(string)

	now := time.Now()
	s.pkceOAuthMu.Lock()
	s.cleanupPKCEOAuthFlowsLocked(now)
	flow, ok := s.pkceOAuthFlows[request.FlowID]
	if !ok || flow.Owner != owner || flow.Provider != provider {
		s.pkceOAuthMu.Unlock()
		apiError(c, http.StatusNotFound, "oauth_flow_not_found", providerDisplayName(provider)+" OAuth login was not found or has expired")
		return
	}
	if returnedState != "" && returnedState != flow.State {
		s.pkceOAuthMu.Unlock()
		apiError(c, http.StatusBadRequest, "oauth_state_mismatch", "OAuth state does not match this login")
		return
	}
	if flow.Finalizing {
		s.pkceOAuthMu.Unlock()
		c.JSON(http.StatusAccepted, gin.H{"status": "finalizing", "retry_after": 1})
		return
	}
	flow.Finalizing = true
	s.pkceOAuthMu.Unlock()

	credentials, expiresAt, oauthErr, err := s.exchangePKCECode(c.Request.Context(), flow, code)
	if err != nil {
		s.setPKCEFinalizing(flow.ID, false)
		if oauthErr.Error == "invalid_grant" {
			s.removePKCEOAuthFlow(flow.ID)
			apiError(c, http.StatusBadRequest, "oauth_invalid_grant", oauthErrorMessage(oauthErr, "authorization code is invalid or has already been used"))
			return
		}
		if oauthErr.Error != "" {
			apiError(c, http.StatusBadGateway, "oauth_rejected", err.Error())
			return
		}
		apiError(c, http.StatusBadGateway, "oauth_unavailable", err.Error())
		return
	}
	if credentials.AccessToken == "" {
		s.setPKCEFinalizing(flow.ID, false)
		apiError(c, http.StatusBadGateway, "oauth_invalid_response", providerDisplayName(provider)+" returned a token response without an access token")
		return
	}
	if provider == model.ProviderCodex && credentials.ChatGPTAccountID == "" {
		s.removePKCEOAuthFlow(flow.ID)
		apiError(c, http.StatusBadGateway, "oauth_invalid_response", "Codex token did not include chatgpt_account_id")
		return
	}

	userID := userFromContext(c).ID
	account, err := s.repo.CreateAccount(c.Request.Context(), createOAuthAccountParams(flow.Account, provider, credentials, expiresAt, userID))
	if err != nil {
		s.setPKCEFinalizing(flow.ID, false)
		apiError(c, http.StatusInternalServerError, "internal_error", "authorization succeeded but the account could not be created; retry the code exchange")
		return
	}
	s.removePKCEOAuthFlow(flow.ID)
	c.JSON(http.StatusCreated, gin.H{"status": "complete", "account": account})
}

func (s *Server) pkceAuthorizationURL(provider model.Provider, state, challenge string) (string, error) {
	switch provider {
	case model.ProviderClaude:
		values := url.Values{
			"code":                  {"true"},
			"client_id":             {s.cfg.ClaudeOAuth.ClientID},
			"response_type":         {"code"},
			"redirect_uri":          {s.cfg.ClaudeOAuth.RedirectURI},
			"scope":                 {strings.Join(s.cfg.ClaudeOAuth.Scopes, " ")},
			"code_challenge":        {challenge},
			"code_challenge_method": {"S256"},
			"state":                 {state},
		}
		return s.cfg.ClaudeOAuth.AuthorizeURL + "?" + values.Encode(), nil
	case model.ProviderCodex:
		values := url.Values{
			"response_type":              {"code"},
			"client_id":                  {s.cfg.CodexOAuth.ClientID},
			"redirect_uri":               {s.cfg.CodexOAuth.RedirectURI},
			"scope":                      {strings.Join(s.cfg.CodexOAuth.Scopes, " ")},
			"code_challenge":             {challenge},
			"code_challenge_method":      {"S256"},
			"state":                      {state},
			"id_token_add_organizations": {"true"},
			"codex_cli_simplified_flow":  {"true"},
		}
		return s.cfg.CodexOAuth.AuthorizeURL + "?" + values.Encode(), nil
	default:
		return "", errors.New("unsupported OAuth provider")
	}
}

func generatePKCE() (verifier, challenge string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func parseAuthorizationInput(raw string) (code, state string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", errors.New("authorization code is required")
	}
	if len(raw) > maxAuthCodeBytes {
		return "", "", errors.New("authorization code is too large")
	}
	if strings.Contains(raw, "://") {
		parsed, parseErr := url.Parse(raw)
		if parseErr != nil {
			return "", "", errors.New("authorization callback URL is invalid")
		}
		code = strings.TrimSpace(parsed.Query().Get("code"))
		state = strings.TrimSpace(parsed.Query().Get("state"))
		if code == "" && parsed.Fragment != "" {
			values, _ := url.ParseQuery(strings.TrimPrefix(parsed.Fragment, "/"))
			code = strings.TrimSpace(values.Get("code"))
			if state == "" {
				state = strings.TrimSpace(values.Get("state"))
			}
		}
	}
	if code == "" {
		var after string
		var found bool
		code, after, found = strings.Cut(raw, "#")
		code = strings.TrimSpace(code)
		if found && state == "" {
			state = strings.TrimSpace(after)
			if queryIdx := strings.IndexAny(state, "&?"); queryIdx >= 0 {
				state = strings.TrimSpace(state[:queryIdx])
			}
		}
	}
	if code == "" {
		return "", "", errors.New("could not find an authorization code")
	}
	if strings.ContainsAny(code, "\r\n") || strings.ContainsAny(state, "\r\n") {
		return "", "", errors.New("authorization code is invalid")
	}
	return code, state, nil
}

func (s *Server) cleanupPKCEOAuthFlowsLocked(now time.Time) {
	for id, flow := range s.pkceOAuthFlows {
		if !now.Before(flow.ExpiresAt) {
			delete(s.pkceOAuthFlows, id)
			closeHTTPClient(flow.Client)
		}
	}
}

func (s *Server) removePKCEOAuthFlow(id string) {
	s.pkceOAuthMu.Lock()
	flow := s.pkceOAuthFlows[id]
	delete(s.pkceOAuthFlows, id)
	s.pkceOAuthMu.Unlock()
	if flow != nil {
		closeHTTPClient(flow.Client)
	}
}

func (s *Server) setPKCEFinalizing(id string, finalizing bool) {
	s.pkceOAuthMu.Lock()
	if flow, ok := s.pkceOAuthFlows[id]; ok {
		flow.Finalizing = finalizing
	}
	s.pkceOAuthMu.Unlock()
}

func providerDisplayName(provider model.Provider) string {
	switch provider {
	case model.ProviderClaude:
		return "Claude"
	case model.ProviderCodex:
		return "Codex"
	case model.ProviderGrok:
		return "Grok"
	default:
		return string(provider)
	}
}
