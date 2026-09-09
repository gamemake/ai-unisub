package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
	"github.com/gin-gonic/gin"
)

// refreshSubscriptionUsage is the provider-neutral entry point for the
// provider-specific usage refresh implementations.
func (s *Server) refreshSubscriptionUsage(c *gin.Context) {
	account, ok := s.subscriptionByParam(c)
	if !ok {
		return
	}
	if account.Provider != model.ProviderGrok && account.Provider != model.ProviderClaude && account.Provider != model.ProviderCodex {
		apiError(c, http.StatusUnprocessableEntity, "usage_refresh_unsupported", "active usage refresh is available only for Claude, Codex, and Grok OAuth accounts")
		return
	}
	if !s.allowRate(fmt.Sprintf("usage-refresh:%d", account.ID), 6, time.Minute) {
		apiError(c, http.StatusTooManyRequests, "rate_limited", "account usage was refreshed too frequently")
		return
	}
	var (
		updated model.Subscription
		err     error
	)
	switch account.Provider {
	case model.ProviderClaude:
		updated, err = s.refreshClaudeQuota(c.Request.Context(), account)
	case model.ProviderCodex:
		updated, err = s.refreshCodexQuota(c.Request.Context(), account)
	default:
		updated, err = s.refreshGrokQuota(c.Request.Context(), account)
	}
	if err != nil {
		apiError(c, http.StatusBadGateway, "usage_refresh_failed", err.Error())
		return
	}
	usage, _ := s.repo.UsageSummary(c.Request.Context(), account.ID)
	c.JSON(http.StatusOK, subscriptionUsageResponse(updated, usage))
}

// storeQuotaError preserves the last good quota while recording the latest
// provider usage refresh failure.
func (s *Server) storeQuotaError(ctx context.Context, account model.Subscription, cause error) error {
	message := strings.TrimSpace(cause.Error())
	if len(message) > 240 {
		message = message[:240]
	}
	_ = s.repo.UpdateSubscriptionQuotaError(ctx, account.ID, message)
	return errors.New(message)
}
