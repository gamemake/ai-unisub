package unisub

import (
	"net/http"

	"ai-unisub/internal/service2"
)

func (m *APIModule) providerOptions(ctx service.ModuleContext, w http.ResponseWriter) {
	accounts := ctx.AIProviders().ListAccounts()
	items := make([]map[string]any, 0, len(accounts))
	for _, account := range accounts {
		clients := []string{}
		if account.Config.ClientType != "" {
			clients = append(clients, string(account.Config.ClientType))
		}
		items = append(items, map[string]any{
			"id": account.ID, "name": account.Config.Name, "provider": account.Config.Kind,
			"enabled": account.Config.Enabled, "client_types": clients,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}
