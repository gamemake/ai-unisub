package unisub

import (
	"ai-unisub/internal/aiprovider"
	"ai-unisub/internal/common"
	framework "ai-unisub/internal/service"
	"errors"
	"net/http"
	"time"
)

func (m *APIModule) fetchAIProviderModels(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request, id int) {
	if !isAdmin(r) {
		common.WriteError(w, http.StatusForbidden, common.MessageForbidden)
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if _, ok := ctx.AIProviders().Get(id); !ok {
		common.WriteError(w, http.StatusNotFound, common.MessageAIProviderNotFound)
		return
	}
	started := time.Now().UTC()
	models, err := ctx.AIProviders().FetchModels(adminCallContext(ctx, r, id, started), id)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, aiprovider.ErrModelsUnsupported) {
			status = http.StatusNotImplemented
		}
		if errors.Is(err, aiprovider.ErrModelsNotConfigured) {
			status = http.StatusBadRequest
		}
		if errors.Is(err, aiprovider.ErrModelsRateLimited) {
			status = http.StatusTooManyRequests
		}
		common.WriteError(w, status, modelsError(err))
		return
	}
	if models == nil {
		models = []aiprovider.ModelInfo{}
	}
	writeJSON(w, http.StatusOK, aiprovider.ModelList{Models: models})
}

func (m *APIModule) listAIProviderModels(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request, id int) {
	if _, ok := currentUser(r); !ok {
		common.WriteError(w, http.StatusUnauthorized, common.MessageUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if _, ok := ctx.AIProviders().Get(id); !ok {
		common.WriteError(w, http.StatusNotFound, common.MessageAIProviderNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, aiprovider.ModelList{Models: ctx.AIProviders().ModelsForProvider(id)})
}

func modelsError(err error) string {
	if errors.Is(err, aiprovider.ErrModelsUnsupported) {
		return "This provider does not support model listing"
	}
	if errors.Is(err, aiprovider.ErrModelsNotConfigured) {
		return "Missing listing credentials or upstream URL"
	}
	if errors.Is(err, aiprovider.ErrModelsAuthentication) {
		return "Model listing authentication failed; check credentials and permissions"
	}
	if errors.Is(err, aiprovider.ErrModelsRateLimited) {
		return "Model listing rate limit exceeded; please try again later"
	}
	if errors.Is(err, aiprovider.ErrModelsInvalidResponse) {
		return "The upstream returned invalid model list data"
	}
	// Never expose raw upstream errors, which may contain credentials.
	return "Model listing failed; please try again later"
}
