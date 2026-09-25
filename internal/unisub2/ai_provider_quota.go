package unisub

import (
	"errors"
	"net/http"

	"ai-unisub/internal/aiprovider2"
	"ai-unisub/internal/common"
	framework "ai-unisub/internal/service2"
)

func (m *APIModule) refreshAIProviderQuota(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request, id int) {
	if !isAdmin(r) {
		common.WriteError(w, http.StatusForbidden, common.MessageForbidden)
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	account := providerAccount(ctx, id)
	if account == nil {
		common.WriteError(w, http.StatusNotFound, common.MessageAIProviderNotFound)
		return
	}
	if account.Config.Kind == aiprovider2.AccountGroup {
		common.WriteError(w, http.StatusNotImplemented, "Group providers do not support quota queries")
		return
	}
	quota, err := ctx.AIProviders().FetchQuota(r.Context(), id)
	if err != nil {
		status := http.StatusBadGateway
		switch {
		case errors.Is(err, aiprovider2.ErrQuotaUnsupported):
			status = http.StatusNotImplemented
		case errors.Is(err, aiprovider2.ErrQuotaNotConfigured):
			status = http.StatusBadRequest
		case errors.Is(err, aiprovider2.ErrRateLimited):
			status = http.StatusTooManyRequests
		}
		common.WriteError(w, status, quotaError(err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, quota)
}

func quotaError(err error) string {
	switch {
	case errors.Is(err, aiprovider2.ErrQuotaUnsupported):
		return "This supplier does not support quota queries"
	case errors.Is(err, aiprovider2.ErrQuotaNotConfigured):
		return "Missing query credentials or endpoint"
	case errors.Is(err, aiprovider2.ErrAuthentication):
		return "Query authentication failed; check credentials and permissions"
	case errors.Is(err, aiprovider2.ErrRateLimited):
		return "Query rate limit exceeded; please try again later"
	case errors.Is(err, aiprovider2.ErrInvalidResponse):
		return "The upstream returned invalid quota data"
	default:
		return "Quota query failed; please try again later"
	}
}
