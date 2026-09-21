package aiprovider

import (
	"errors"
	"maps"
	"strings"
)

// Subscription plan IDs are stable config values (not observed from upstream).
const (
	PlanCodexPlus      = "codex_plus"
	PlanCodexPro5x     = "codex_pro_5x"
	PlanCodexPro20x    = "codex_pro_20x"
	PlanClaudePro      = "claude_pro"
	PlanClaudeMax5x    = "claude_max_5x"
	PlanClaudeMax20x   = "claude_max_20x"
	PlanSuperGrok      = "super_grok"
	PlanSuperGrokPlus  = "super_grok_plus"
	PlanSuperGrokHeavy = "super_grok_heavy"
)

// SubscriptionPlanInfo is a code-owned catalog entry for UI and validation.
type SubscriptionPlanInfo struct {
	ID    string
	Label string
}

var (
	codexPlans = []SubscriptionPlanInfo{
		{ID: PlanCodexPlus, Label: "Plus"},
		{ID: PlanCodexPro5x, Label: "Pro 5x"},
		{ID: PlanCodexPro20x, Label: "Pro 20x"},
	}
	claudePlans = []SubscriptionPlanInfo{
		{ID: PlanClaudePro, Label: "Pro"},
		{ID: PlanClaudeMax5x, Label: "Max 5x"},
		{ID: PlanClaudeMax20x, Label: "Max 20x"},
	}
	grokPlans = []SubscriptionPlanInfo{
		{ID: PlanSuperGrok, Label: "SuperGrok"},
		{ID: PlanSuperGrokPlus, Label: "SuperGrok Plus"},
		{ID: PlanSuperGrokHeavy, Label: "SuperGrok Heavy"},
	}
)

// SubscriptionPlansForAdapter returns the plan catalog for a subscription adapter.
// Dummy uses the same catalog as Claude. Unknown adapters return nil.
func SubscriptionPlansForAdapter(adapter string) []SubscriptionPlanInfo {
	switch strings.ToLower(strings.TrimSpace(adapter)) {
	case "codex":
		return append([]SubscriptionPlanInfo(nil), codexPlans...)
	case "claude", "dummy":
		return append([]SubscriptionPlanInfo(nil), claudePlans...)
	case "grok":
		return append([]SubscriptionPlanInfo(nil), grokPlans...)
	default:
		return nil
	}
}

// SubscriptionPlansForSupplier returns plans owned by a model-supplier id.
func SubscriptionPlansForSupplier(supplierID string) []SubscriptionPlanInfo {
	switch strings.TrimSpace(supplierID) {
	case "openai":
		return SubscriptionPlansForAdapter("codex")
	case "anthropic":
		return SubscriptionPlansForAdapter("claude")
	case "grok":
		return SubscriptionPlansForAdapter("grok")
	default:
		return nil
	}
}

// DefaultSubscriptionPlan returns the default plan ID when config is empty on load.
func DefaultSubscriptionPlan(adapter string) string {
	switch strings.ToLower(strings.TrimSpace(adapter)) {
	case "codex":
		return PlanCodexPlus
	case "claude", "dummy":
		return PlanClaudePro
	case "grok":
		return PlanSuperGrok
	default:
		return ""
	}
}

// SubscriptionPlanLabel returns the display label for a known plan ID.
func SubscriptionPlanLabel(id string) string {
	id = strings.TrimSpace(id)
	for _, plans := range [][]SubscriptionPlanInfo{codexPlans, claudePlans, grokPlans} {
		for _, p := range plans {
			if p.ID == id {
				return p.Label
			}
		}
	}
	return id
}

// BuiltinSubscriptionPlanWeights returns code defaults for a supplier (flat map).
// Callers must clone before mutating. Suppliers without subscription plans return nil.
func BuiltinSubscriptionPlanWeights(supplierID string) map[string]int {
	switch strings.TrimSpace(supplierID) {
	case "openai":
		return map[string]int{
			PlanCodexPlus:   1,
			PlanCodexPro5x:  5,
			PlanCodexPro20x: 20,
		}
	case "anthropic":
		return map[string]int{
			PlanClaudePro:    1,
			PlanClaudeMax5x:  5,
			PlanClaudeMax20x: 20,
		}
	case "grok":
		return map[string]int{
			PlanSuperGrok:      1,
			PlanSuperGrokPlus:  3,
			PlanSuperGrokHeavy: 10,
		}
	default:
		return nil
	}
}

// UsageWeight returns the relative usage weight for a plan using builtin defaults.
// Prefer AIProviderManager.UsageWeight when catalog overlays may apply.
func UsageWeight(supplierID, plan string) int {
	return usageWeightFromMap(BuiltinSubscriptionPlanWeights(supplierID), supplierID, plan)
}

// UsageWeightFromConfig maps an account config to a usage weight using builtins.
// API accounts return 1. Dummy reads anthropic weights.
func UsageWeightFromConfig(adapter string, c AIProviderConfig) int {
	return usageWeightFromConfig(adapter, c, nil)
}

// UsageWeight returns catalog-merged weights for supplierID+plan (fallback builtins).
func (m *AIProviderManager) UsageWeight(supplierID, plan string) int {
	if m == nil {
		return UsageWeight(supplierID, plan)
	}
	if s, ok := m.Supplier(supplierID); ok && len(s.SubscriptionPlanWeights) > 0 {
		return usageWeightFromMap(s.SubscriptionPlanWeights, supplierID, plan)
	}
	return UsageWeight(supplierID, plan)
}

// UsageWeightFromConfig uses the manager catalog when available.
func (m *AIProviderManager) UsageWeightFromConfig(adapter string, c AIProviderConfig) int {
	return usageWeightFromConfig(adapter, c, m)
}

func usageWeightFromConfig(adapter string, c AIProviderConfig, m *AIProviderManager) int {
	kind := strings.TrimSpace(c.Kind)
	if kind == "" {
		if c.AuthType == AuthTypeAPIKey {
			kind = "api"
		} else {
			kind = "subscription"
		}
	}
	if kind == "api" || kind == "group" {
		return 1
	}
	adapter = strings.ToLower(strings.TrimSpace(adapter))
	supplierID := SupplierIDForAccount(c, adapter)
	if adapter == "dummy" {
		supplierID = "anthropic"
	}
	plan := strings.TrimSpace(c.SubscriptionPlan)
	if plan == "" {
		plan = DefaultSubscriptionPlan(adapter)
	}
	if m != nil {
		return m.UsageWeight(supplierID, plan)
	}
	return UsageWeight(supplierID, plan)
}

func usageWeightFromMap(weights map[string]int, supplierID, plan string) int {
	plan = strings.TrimSpace(plan)
	if plan == "" {
		// Resolve default via supplier → adapter mapping for empty plan.
		switch strings.TrimSpace(supplierID) {
		case "openai":
			plan = PlanCodexPlus
		case "anthropic":
			plan = PlanClaudePro
		case "grok":
			plan = PlanSuperGrok
		}
	}
	if w, ok := weights[plan]; ok && w >= 1 {
		return w
	}
	// Fall back to builtin default plan weight, then 1.
	builtin := BuiltinSubscriptionPlanWeights(supplierID)
	if w, ok := builtin[plan]; ok && w >= 1 {
		return w
	}
	var fallback string
	switch strings.TrimSpace(supplierID) {
	case "openai":
		fallback = PlanCodexPlus
	case "anthropic":
		fallback = PlanClaudePro
	case "grok":
		fallback = PlanSuperGrok
	}
	if w, ok := builtin[fallback]; ok && w >= 1 {
		return w
	}
	if w, ok := weights[fallback]; ok && w >= 1 {
		return w
	}
	return 1
}

func validateSubscriptionPlanWeights(supplierID string, weights map[string]int) error {
	allowed := SubscriptionPlansForSupplier(supplierID)
	if len(allowed) == 0 {
		if len(weights) > 0 {
			return errors.New("subscription_plan_weights not supported for this supplier")
		}
		return nil
	}
	known := make(map[string]bool, len(allowed))
	for _, p := range allowed {
		known[p.ID] = true
	}
	for id, w := range weights {
		if !known[id] {
			return errors.New("invalid subscription_plan_weights key")
		}
		if w < 1 {
			return errors.New("subscription plan weight must be at least 1")
		}
	}
	return nil
}

// normalizeSubscriptionPlanWeights fills missing keys from builtin and drops unknowns after validate.
func normalizeSubscriptionPlanWeights(supplierID string, desired map[string]int) (map[string]int, error) {
	builtin := BuiltinSubscriptionPlanWeights(supplierID)
	if builtin == nil {
		if len(desired) > 0 {
			return nil, errors.New("subscription_plan_weights not supported for this supplier")
		}
		return nil, nil
	}
	if err := validateSubscriptionPlanWeights(supplierID, desired); err != nil {
		return nil, err
	}
	out := maps.Clone(builtin)
	for k, v := range desired {
		out[k] = v
	}
	// Ensure every builtin key present (desired may be partial).
	for k, v := range builtin {
		if _, ok := out[k]; !ok {
			out[k] = v
		}
	}
	return out, nil
}

func diffSubscriptionPlanWeights(supplierID string, desired map[string]int) (map[string]int, error) {
	effective, err := normalizeSubscriptionPlanWeights(supplierID, desired)
	if err != nil {
		return nil, err
	}
	builtin := BuiltinSubscriptionPlanWeights(supplierID)
	if sameIntMap(effective, builtin) {
		return nil, nil
	}
	// Store full effective map when any weight differs (flat overlay blob).
	return effective, nil
}

func sameIntMap(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func normalizeSubscriptionPlan(adapter string, kind, plan string) (string, error) {
	plan = strings.TrimSpace(plan)
	if kind != "subscription" {
		if plan != "" {
			return "", errors.New("subscription_plan is only valid for subscription providers")
		}
		return "", nil
	}
	plans := SubscriptionPlansForAdapter(adapter)
	if len(plans) == 0 {
		if plan != "" {
			return "", errors.New("subscription_plan is not supported for this provider")
		}
		return "", nil
	}
	if plan == "" {
		return DefaultSubscriptionPlan(adapter), nil
	}
	for _, p := range plans {
		if p.ID == plan {
			return plan, nil
		}
	}
	return "", errors.New("invalid subscription_plan")
}
