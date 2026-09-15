package unisub

import (
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
		items = append(items, map[string]any{"id": account.ID, "name": account.Name, "provider": account.AIProvider, "enabled": accountEnabled(account.Config)})
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": len(items)})
}
