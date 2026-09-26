package unisub

import (
	"errors"
	"net/http"

	aiprovider "ai-unisub/internal/aiprovider"
	framework "ai-unisub/internal/service"
)

func (m *APIModule) refreshAccountQuota(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request, id int) {
	if !isAdmin(r) {
		WriteError(w, http.StatusForbidden, MessageForbidden)
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	account := providerAccount(ctx, id)
	if account == nil {
		WriteError(w, http.StatusNotFound, MessageAIProviderNotFound)
		return
	}
	if account.Config.Kind == aiprovider.AccountGroup {
		WriteError(w, http.StatusNotImplemented, "Group providers do not support quota queries")
		return
	}
	quota, err := ctx.AIProviders().FetchQuota(r.Context(), id)
	if err != nil {
		status := http.StatusBadGateway
		switch {
		case errors.Is(err, aiprovider.ErrQuotaUnsupported):
			status = http.StatusNotImplemented
		case errors.Is(err, aiprovider.ErrQuotaNotConfigured):
			status = http.StatusBadRequest
		case errors.Is(err, aiprovider.ErrRateLimited):
			status = http.StatusTooManyRequests
		}
		WriteError(w, status, quotaError(err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, quota)
}

func quotaError(err error) string {
	switch {
	case errors.Is(err, aiprovider.ErrQuotaUnsupported):
		return "This supplier does not support quota queries"
	case errors.Is(err, aiprovider.ErrQuotaNotConfigured):
		return "Missing query credentials or endpoint"
	case errors.Is(err, aiprovider.ErrAuthentication):
		return "Query authentication failed; check credentials and permissions"
	case errors.Is(err, aiprovider.ErrRateLimited):
		return "Query rate limit exceeded; please try again later"
	case errors.Is(err, aiprovider.ErrInvalidResponse):
		return "The upstream returned invalid quota data"
	default:
		return "Quota query failed; please try again later"
	}
}
