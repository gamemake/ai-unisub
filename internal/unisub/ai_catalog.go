package unisub

import (
	"ai-unisub/internal/aiprovider"
	"ai-unisub/internal/common"
	"ai-unisub/internal/service"
	"net/http"
	"strings"
)

func (m *APIModule) aiCatalog(ctx service.ModuleContext, w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) < 1 || len(parts) > 2 {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodGet {
		if len(parts) == 2 {
			supplier, ok := ctx.AIProviders().Supplier(parts[1])
			if !ok {
				http.NotFound(w, r)
				return
			}
			writeJSON(w, 200, supplier)
			return
		}
		writeJSON(w, 200, map[string]any{"catalog": ctx.AIProviders().Catalog(), "builtin_suppliers": aiprovider.BuiltinSuppliers()})
		return
	}
	if !isAdmin(r) {
		common.WriteError(w, 403, common.MessageForbidden)
		return
	}
	// Batch catalog PUT is not supported; only per-supplier updates.
	if len(parts) != 2 {
		w.Header().Set("Allow", "GET")
		w.WriteHeader(405)
		return
	}
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", "GET, PUT")
		w.WriteHeader(405)
		return
	}
	var body struct {
		ID                                   string            `json:"id"`
		Name                                 string            `json:"name"`
		Models                               []string          `json:"models"`
		SubscriptionUsageHeaderOverrides     map[string]string `json:"subscription_usage_header_overrides"`
		APIUsageHeaderOverrides              map[string]string `json:"api_usage_header_overrides"`
		ClaudeURL                            *string           `json:"claude_url"`
		OpenAIURL                            *string           `json:"openai_url"`
		CodexURL                             *string           `json:"codex_url"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.ID != "" && body.ID != parts[1] {
		common.WriteError(w, 400, "supplier ID must match the URL")
		return
	}
	if body.ClaudeURL != nil || body.OpenAIURL != nil || body.CodexURL != nil {
		common.WriteError(w, 400, "built-in supplier URLs cannot be modified")
		return
	}
	desired := aiprovider.SupplierConfigurable{
		Name:                             strings.TrimSpace(body.Name),
		Models:                           body.Models,
		SubscriptionUsageHeaderOverrides: body.SubscriptionUsageHeaderOverrides,
		APIUsageHeaderOverrides:          body.APIUsageHeaderOverrides,
	}
	if desired.Models == nil {
		// Missing models key: keep current effective models so name-only edits work.
		if current, ok := ctx.AIProviders().Supplier(parts[1]); ok {
			desired.Models = current.Models
		}
	}
	m.mutations.Lock()
	defer m.mutations.Unlock()
	supplier, err := ctx.AIProviders().UpdateSupplier(parts[1], desired)
	if err != nil {
		if err.Error() == "unknown supplier" {
			http.NotFound(w, r)
			return
		}
		common.WriteError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, supplier)
}
