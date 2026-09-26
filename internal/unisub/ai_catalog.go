package unisub

import (
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"

	aiprovider "ai-unisub/internal/aiprovider"
	"ai-unisub/internal/service"
)

// supplierView is the supplier shape the admin pages consume. Mappings use
// from/to and plan weights are a plan ID → weight map.
type supplierView struct {
	ID                      string                  `json:"id"`
	Name                    string                  `json:"name"`
	ClaudeURL               string                  `json:"claude_url"`
	OpenAIURL               string                  `json:"openai_url"`
	Models                  []string                `json:"models"`
	ModelMappings           []modelMappingView      `json:"model_mappings"`
	SupportedClients        []aiprovider.ClientType `json:"supported_clients"`
	SubscriptionPlanWeights map[string]int          `json:"subscription_plan_weights,omitempty"`
}

type modelMappingView struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (m *APIModule) suppliers(ctx service.ModuleContext, w http.ResponseWriter, r *http.Request, parts []string) {
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
			writeJSON(w, http.StatusOK, supplierResponse(supplier, supplier.GetConfig()))
			return
		}
		suppliers := ctx.AIProviders().ListSuppliers()
		items := make([]supplierView, 0, len(suppliers))
		builtins := make([]supplierView, 0, len(suppliers))
		for _, supplier := range suppliers {
			items = append(items, supplierResponse(supplier, supplier.GetConfig()))
			builtins = append(builtins, supplierResponse(supplier, supplier.GetBuiltinConfig()))
		}
		writeJSON(w, http.StatusOK, map[string]any{"suppliers": items, "builtin_suppliers": builtins})
		return
	}
	if !isAdmin(r) {
		WriteError(w, http.StatusForbidden, MessageForbidden)
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
		ID                      string             `json:"id"`
		Models                  []string           `json:"models"`
		ModelMappings           []modelMappingView `json:"model_mappings"`
		SubscriptionPlanWeights map[string]int     `json:"subscription_plan_weights"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.ID != "" && !strings.EqualFold(input.ID, parts[1]) {
		WriteError(w, http.StatusBadRequest, "supplier ID must match the URL")
		return
	}
	// Omitted fields keep the current effective value; the stored overlay is
	// the difference from the built-in config.
	config := supplier.GetConfig()
	if input.Models != nil {
		config.Models = input.Models
	}
	if input.ModelMappings != nil {
		config.Mappings = make([]aiprovider.ModelMapping, len(input.ModelMappings))
		for i, mapping := range input.ModelMappings {
			config.Mappings[i] = aiprovider.ModelMapping{Pattern: mapping.From, Target: mapping.To}
		}
	}
	if input.SubscriptionPlanWeights != nil {
		config.Weights = planWeightsFromMap(input.SubscriptionPlanWeights, supplier.GetBuiltinConfig().Weights)
	}
	overlay, err := supplier.DiffConfig(config)
	if err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	raw, err := json.Marshal(overlay)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, MessageInternalServerError)
		return
	}
	m.mutations.Lock()
	err = ctx.AIProviders().SetOverlayConfig(r.Context(), parts[1], raw)
	m.mutations.Unlock()
	if err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, supplierResponse(supplier, supplier.GetConfig()))
}

// planWeightsFromMap orders weights like the built-in list so an unchanged
// map produces no overlay; unknown plans follow in name order.
func planWeightsFromMap(weights map[string]int, builtin []aiprovider.SubscriptionPlanWeight) []aiprovider.SubscriptionPlanWeight {
	result := make([]aiprovider.SubscriptionPlanWeight, 0, len(weights))
	for _, plan := range builtin {
		if weight, ok := weights[plan.Name]; ok {
			result = append(result, aiprovider.SubscriptionPlanWeight{Name: plan.Name, Weight: weight})
		}
	}
	for _, name := range slices.Sorted(maps.Keys(weights)) {
		if !slices.ContainsFunc(builtin, func(plan aiprovider.SubscriptionPlanWeight) bool { return plan.Name == name }) {
			result = append(result, aiprovider.SubscriptionPlanWeight{Name: name, Weight: weights[name]})
		}
	}
	return result
}

func findSupplier(ctx service.ModuleContext, id string) aiprovider.Supplier {
	for _, supplier := range ctx.AIProviders().ListSuppliers() {
		if strings.EqualFold(supplier.GetID(), id) {
			return supplier
		}
	}
	return nil
}

func supplierResponse(supplier aiprovider.Supplier, config aiprovider.SupplierConfig) supplierView {
	mappings := make([]modelMappingView, len(config.Mappings))
	for i, mapping := range config.Mappings {
		mappings[i] = modelMappingView{From: mapping.Pattern, To: mapping.Target}
	}
	var weights map[string]int
	if len(config.Weights) > 0 {
		weights = make(map[string]int, len(config.Weights))
		for _, plan := range config.Weights {
			weights[plan.Name] = plan.Weight
		}
	}
	clients := supplier.SupportClients()
	if clients == nil {
		clients = []aiprovider.ClientType{}
	}
	models := config.Models
	if models == nil {
		models = []string{}
	}
	return supplierView{
		ID: supplier.GetID(), Name: supplier.GetName(), ClaudeURL: config.ClaudeURL, OpenAIURL: config.OpenAIURL,
		Models: models, ModelMappings: mappings, SupportedClients: clients, SubscriptionPlanWeights: weights,
	}
}
