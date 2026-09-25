package unisub

import (
	"encoding/json"
	"net/http"
	"strings"

	"ai-unisub/internal/aiprovider2"
	"ai-unisub/internal/common"
	"ai-unisub/internal/service2"
)

type supplierView struct {
	ID                      string                               `json:"id"`
	Name                    string                               `json:"name"`
	Models                  []string                             `json:"models"`
	ModelMappings           []aiprovider2.ModelMapping           `json:"model_mappings"`
	SubscriptionPlanWeights []aiprovider2.SubscriptionPlanWeight `json:"subscription_plan_weights"`
	ClaudeURL               string                               `json:"claude_url,omitempty"`
	OpenAIURL               string                               `json:"openai_url,omitempty"`
}

func (m *APIModule) aiCatalog(ctx service.ModuleContext, w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) < 1 || len(parts) > 2 {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodGet {
		if len(parts) == 2 {
			supplier := findSupplier(ctx, parts[1])
			if supplier == nil {
				http.NotFound(w, r)
				return
			}
			writeJSON(w, http.StatusOK, supplierResponse(supplier))
			return
		}
		items := make([]supplierView, 0)
		for _, supplier := range ctx.AIProviders().ListSuppliers() {
			items = append(items, supplierResponse(supplier))
		}
		writeJSON(w, http.StatusOK, map[string]any{"suppliers": items})
		return
	}
	if !isAdmin(r) {
		common.WriteError(w, http.StatusForbidden, common.MessageForbidden)
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPut {
		methodNotAllowed(w)
		return
	}
	supplier := findSupplier(ctx, parts[1])
	if supplier == nil {
		http.NotFound(w, r)
		return
	}
	var input struct {
		ID                      string                               `json:"id"`
		Models                  []string                             `json:"models"`
		ModelMappings           []aiprovider2.ModelMapping           `json:"model_mappings"`
		SubscriptionPlanWeights []aiprovider2.SubscriptionPlanWeight `json:"subscription_plan_weights"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.ID != "" && !strings.EqualFold(input.ID, parts[1]) {
		common.WriteError(w, http.StatusBadRequest, "supplier ID must match the URL")
		return
	}
	overlay := aiprovider2.SupplierOverlayConfig{
		Models: input.Models, Mappings: input.ModelMappings, Weights: input.SubscriptionPlanWeights,
	}
	raw, err := json.Marshal(overlay)
	if err != nil {
		common.WriteError(w, http.StatusInternalServerError, common.MessageInternalServerError)
		return
	}
	m.mutations.Lock()
	err = ctx.AIProviders().SetOverlayConfig(r.Context(), parts[1], raw)
	m.mutations.Unlock()
	if err != nil {
		common.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, supplierResponse(supplier))
}

func findSupplier(ctx service.ModuleContext, id string) aiprovider2.Supplier {
	for _, supplier := range ctx.AIProviders().ListSuppliers() {
		if strings.EqualFold(supplier.GetID(), id) {
			return supplier
		}
	}
	return nil
}

func supplierResponse(supplier aiprovider2.Supplier) supplierView {
	config := supplier.GetConfig()
	return supplierView{
		ID: supplier.GetID(), Name: supplier.GetName(), Models: config.Models,
		ModelMappings: config.Mappings, SubscriptionPlanWeights: config.Weights,
		ClaudeURL: config.ClaudeURL, OpenAIURL: config.OpenAIURL,
	}
}
