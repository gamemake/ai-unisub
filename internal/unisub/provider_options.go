package unisub

import (
	"ai-unisub/internal/aiprovider"
	"ai-unisub/internal/common"
	"ai-unisub/internal/service"
	"net/http"
)

// providerOptions supplies only display/binding metadata needed by personal pages.
// Provider configuration, credentials, endpoints and group members stay admin-only.
func (m *APIModule) providerOptions(ctx service.ModuleContext, w http.ResponseWriter) {
	accounts, err := ctx.Database().ListAccounts()
	if err != nil {
		common.WriteError(w, 500, common.MessageCouldNotListAIProviders)
		return
	}
	items := make([]map[string]any, 0, len(accounts))
	for _, account := range accounts {
		clients := ctx.AIProviders().SupportedClientsForProvider(account.ID)
		items = append(items, map[string]any{
			"id": account.ID, "name": account.Name, "provider": account.AIProvider,
			"enabled": accountEnabled(account.Config), "client_types": clientTypesJSON(clients),
		})
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": len(items)})
}

func clientTypesJSON(clients []aiprovider.ClientType) []string {
	if clients == nil {
		return []string{}
	}
	out := make([]string, len(clients))
	for i, c := range clients {
		out[i] = string(c)
	}
	return out
}
