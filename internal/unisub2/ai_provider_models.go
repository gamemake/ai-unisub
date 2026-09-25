package unisub

import (
	"errors"
	"net/http"

	"ai-unisub/internal/aiprovider2"
	"ai-unisub/internal/common"
	framework "ai-unisub/internal/service2"
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
	account := providerAccount(ctx, id)
	if account == nil {
		common.WriteError(w, http.StatusNotFound, common.MessageAIProviderNotFound)
		return
	}
	if err := ctx.AIProviders().RefreshModels(r.Context(), account.Config.Supplier, id); err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, aiprovider2.ErrModelsUnsupported) {
			status = http.StatusNotImplemented
		}
		common.WriteError(w, status, modelsError(err))
		return
	}
	models, err := ctx.AIProviders().GetModels(r.Context(), id, string(account.Config.ClientType))
	if err != nil {
		common.WriteError(w, http.StatusBadGateway, modelsError(err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
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
	account := providerAccount(ctx, id)
	if account == nil {
		common.WriteError(w, http.StatusNotFound, common.MessageAIProviderNotFound)
		return
	}
	models, err := ctx.AIProviders().GetModels(r.Context(), id, string(account.Config.ClientType))
	if err != nil {
		common.WriteError(w, http.StatusBadGateway, modelsError(err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

func modelsError(err error) string {
	if errors.Is(err, aiprovider2.ErrModelsUnsupported) {
		return "This provider does not support model listing"
	}
	return "Model listing failed; please try again later"
}

func providerAccount(ctx framework.ModuleContext, id int) *aiprovider2.Account {
	for _, account := range ctx.AIProviders().ListAccounts() {
		if account.ID == id {
			return account
		}
	}
	return nil
}
