package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
	"github.com/gin-gonic/gin"
)

type oauthSubscriptionRequest struct {
	Name                           string          `json:"name"`
	Metadata                       json.RawMessage `json:"metadata"`
	ConcurrencyLimit               int             `json:"concurrency_limit"`
	ConcurrencyQueueTimeoutSeconds int             `json:"concurrency_queue_timeout_seconds"`
	ProxyURL                       string          `json:"proxy_url"`
}

func (s *Server) bindOAuthSubscriptionRequest(c *gin.Context) (oauthSubscriptionRequest, *http.Client, bool) {
	var account oauthSubscriptionRequest
	if c.ShouldBindJSON(&account) != nil || strings.TrimSpace(account.Name) == "" {
		apiError(c, http.StatusBadRequest, "invalid_request", "account name is required")
		return oauthSubscriptionRequest{}, nil, false
	}
	account.Name = strings.TrimSpace(account.Name)
	if len(account.Metadata) > 0 && !json.Valid(account.Metadata) {
		apiError(c, http.StatusBadRequest, "invalid_request", "metadata must be valid JSON")
		return oauthSubscriptionRequest{}, nil, false
	}
	if err := validateConcurrencyQueueTimeout(account.ConcurrencyQueueTimeoutSeconds); err != nil {
		apiError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return oauthSubscriptionRequest{}, nil, false
	}
	proxyURL, err := normalizeProxyURL(account.ProxyURL)
	if err != nil {
		apiError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return oauthSubscriptionRequest{}, nil, false
	}
	account.ProxyURL = proxyURL
	client, err := newClientForProxy(proxyURL)
	if err != nil {
		apiError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return oauthSubscriptionRequest{}, nil, false
	}
	return account, client, true
}

func oauthRefreshSkew(provider model.Provider) time.Duration {
	switch provider {
	case model.ProviderClaude, model.ProviderCodex:
		return 10 * time.Minute
	default:
		return time.Minute
	}
}

func (s *Server) credentialsForRequest(ctx context.Context, account model.Subscription, credentials model.Credentials, force bool) (model.Credentials, error) {
	switch account.Provider {
	case model.ProviderGrok:
		return s.grokCredentialsForRequest(ctx, account, credentials, force)
	case model.ProviderClaude:
		return s.claudeCredentialsForRequest(ctx, account, credentials, force)
	case model.ProviderCodex:
		return s.codexCredentialsForRequest(ctx, account, credentials, force)
	default:
		return credentials, nil
	}
}
