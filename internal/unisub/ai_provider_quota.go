package unisub

import (
	"ai-unisub/internal/aiprovider"
	"ai-unisub/internal/common"
	framework "ai-unisub/internal/service"
	"errors"
	"net/http"
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
	w.Header().Set("Cache-Control", "no-store")
	provider, ok := ctx.AIProviders().Get(id)
	if !ok {
		common.WriteError(w, http.StatusNotFound, common.MessageAIProviderNotFound)
		return
	}
	if provider.Config().Kind == "group" {
		common.WriteError(w, http.StatusNotImplemented, "Group providers do not support quota queries")
		return
	}
	result, err := provider.FetchQuota(r.Context())
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, aiprovider.ErrQuotaNotImplemented) || errors.Is(err, aiprovider.ErrQuotaUnsupported) {
			status = http.StatusNotImplemented
		}
		if errors.Is(err, aiprovider.ErrQuotaNotConfigured) {
			status = http.StatusBadRequest
		}
		if errors.Is(err, aiprovider.ErrQuotaRateLimited) {
			status = http.StatusTooManyRequests
		}
		if errors.Is(err, aiprovider.ErrQuotaPersist) {
			status = http.StatusInternalServerError
		}
		common.WriteError(w, status, quotaError(err))
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func quotaError(err error) string {
	if errors.Is(err, aiprovider.ErrQuotaSuperseded) {
		return "Configuration or usage has changed; please query again"
	}
	if errors.Is(err, aiprovider.ErrQuotaUnsupported) {
		return "This supplier does not support subscription quota queries"
	}
	if errors.Is(err, aiprovider.ErrQuotaNotConfigured) {
		return "Missing query credentials or account ID, or queries are unsupported for this custom upstream"
	}
	if errors.Is(err, aiprovider.ErrQuotaAuthentication) {
		return "Query authentication failed; check credentials and permissions"
	}
	if errors.Is(err, aiprovider.ErrQuotaRateLimited) {
		return "Query rate limit exceeded; please try again later"
	}
	if errors.Is(err, aiprovider.ErrQuotaInvalidResponse) {
		return "The upstream returned invalid quota data"
	}
	if errors.Is(err, aiprovider.ErrQuotaPersist) {
		return common.MessageCouldNotSaveAIProvider
	}
	if errors.Is(err, aiprovider.ErrQuotaNotImplemented) {
		return "Quota queries are not implemented for this supplier"
	}
	// Never expose raw upstream errors, which may contain credentials.
	return "Quota query failed; please try again later"
}
