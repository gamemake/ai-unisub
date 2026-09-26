package unisub

import (
	"ai-unisub/internal/service"
	"net/http"
)

func (m *APIModule) providerOptions(ctx service.ModuleContext, w http.ResponseWriter) {
	accounts := ctx.AIProviders().ListAccounts()
	items := make([]map[string]any, 0, len(accounts))
	for _, account := range accounts {
		clients, _ := account.SupportedClients()
		items = append(items, map[string]any{
			"id": account.ID, "name": account.Config.Name, "provider": accountProvider(account.Config),
			"enabled": account.Config.Enabled, "client_types": clientTypesJSON(clients),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}
