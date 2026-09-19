package unisub

import (
	"ai-unisub/internal/aiprovider"
	"ai-unisub/internal/common"
	"ai-unisub/internal/database"
	framework "ai-unisub/internal/service"
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// recordAdminProviderCall persists one upstream exchange from admin quota /
// model listing. Original and outbound request headers both store the outbound
// (upstream) headers; there is no separate browser-request header set.
func recordAdminProviderCall(ctx framework.ModuleContext, r *http.Request, accountID int, providerType string, started time.Time, trace *aiprovider.AIProviderCallTrace) {
	if trace == nil {
		return
	}
	requestID, _ := framework.RequestIDFromContext(r.Context())
	sourceIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || sourceIP == "" {
		sourceIP = r.RemoteAddr
	}
	url := trace.URL
	if url == "" {
		url = r.URL.RequestURI()
	}
	outbound := redactedHeaders(trace.OutboundRequestHeaders)
	saved := &database.PersistedCallTrace{
		AccountID:              accountID,
		AIProviderType:         providerType,
		RequestID:              requestID,
		SourceIP:               sourceIP,
		URL:                    url,
		OutboundURL:            url,
		HTTPErrorCode:          persistedCallHTTPCode(trace),
		OriginalRequestHeaders: outbound,
		OutboundRequestHeaders: outbound,
		RequestBody:            trace.RequestBody,
		ResponseHeaders:        redactedHeaders(trace.ResponseHeaders),
		ResponseBody:           trace.ResponseBody,
		Model:                  trace.Model,
		InputTokens:            trace.InputTokens,
		OutputTokens:           trace.OutputTokens,
		CacheCreationTokens:    trace.CacheCreationTokens,
		CacheReadTokens:        trace.CacheReadTokens,
		StartedAt:              started,
		FinishedAt:             time.Now().UTC(),
	}
	if trace.HTTPErrorInfo != "" {
		saved.HTTPErrorInfo = common.MessageUpstreamRequestFailed
	}
	if err := ctx.Database().RecordCallTrace(saved); err != nil {
		common.ModuleLogger("api").Error("record_call_failed", fmt.Sprintf("record admin provider call %s: %v", requestID, err))
	}
}

func accountProviderType(ctx framework.ModuleContext, accountID int) string {
	accounts, err := ctx.Database().ListAccounts()
	if err != nil {
		return ""
	}
	for _, account := range accounts {
		if account.ID == accountID {
			return account.AIProvider
		}
	}
	return ""
}

func adminCallContext(ctx framework.ModuleContext, r *http.Request, accountID int, started time.Time) context.Context {
	providerType := accountProviderType(ctx, accountID)
	return aiprovider.WithAPICallRecorder(r.Context(), func(trace *aiprovider.AIProviderCallTrace) {
		recordAdminProviderCall(ctx, r, accountID, providerType, started, trace)
	})
}
