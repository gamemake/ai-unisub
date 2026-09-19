package aiprovider

import (
	"encoding/json"
	"testing"
)

func TestBuiltinSubscriptionPlanWeights(t *testing.T) {
	cases := map[string]map[string]int{
		"openai":    {PlanCodexPlus: 1, PlanCodexPro5x: 5, PlanCodexPro20x: 20},
		"anthropic": {PlanClaudePro: 1, PlanClaudeMax: 20},
		"grok":      {PlanSuperGrok: 1, PlanSuperGrokPlus: 3, PlanSuperGrokHeavy: 10},
		"deepseek":  nil,
	}
	for id, want := range cases {
		got := BuiltinSubscriptionPlanWeights(id)
		if !sameIntMap(got, want) {
			t.Fatalf("%s: got %#v want %#v", id, got, want)
		}
	}
}

func TestUsageWeightBuiltins(t *testing.T) {
	if UsageWeight("anthropic", PlanClaudeMax) != 20 {
		t.Fatal("claude_max weight")
	}
	if UsageWeight("openai", PlanCodexPro5x) != 5 || UsageWeight("openai", PlanCodexPro20x) != 20 {
		t.Fatal("codex weights")
	}
	if UsageWeight("openai", "") != 1 {
		t.Fatal("empty plan uses default plus")
	}
	if UsageWeightFromConfig("dummy", AIProviderConfig{Kind: "subscription", SubscriptionPlan: PlanClaudeMax}) != 20 {
		t.Fatal("dummy uses anthropic weights")
	}
	if UsageWeightFromConfig("api", AIProviderConfig{Kind: "api", AuthType: AuthTypeAPIKey}) != 1 {
		t.Fatal("api baseline")
	}
}

func TestSupplierPlanWeightsOverlay(t *testing.T) {
	store := &memOverlayStore{data: map[string]json.RawMessage{}}
	m := NewAIProviderManager()
	if err := m.SetOverlayStore(store); err != nil {
		t.Fatal(err)
	}
	b, _ := builtinByID("anthropic")
	got, err := m.UpdateSupplier("anthropic", SupplierConfigurable{
		Name:                    b.Name,
		Models:                  append([]string(nil), b.Models...),
		SubscriptionPlanWeights: map[string]int{PlanClaudePro: 2, PlanClaudeMax: 40},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.SubscriptionPlanWeights[PlanClaudePro] != 2 || got.SubscriptionPlanWeights[PlanClaudeMax] != 40 {
		t.Fatalf("merged weights: %#v", got.SubscriptionPlanWeights)
	}
	if m.UsageWeight("anthropic", PlanClaudeMax) != 40 {
		t.Fatal("manager weight")
	}
	// models-only update must keep weights
	got, err = m.UpdateSupplier("anthropic", SupplierConfigurable{
		Name:   b.Name,
		Models: []string{"only-one"},
	})
	if err != nil || got.Models[0] != "only-one" {
		t.Fatal(err, got)
	}
	if got.SubscriptionPlanWeights[PlanClaudeMax] != 40 {
		t.Fatalf("weights wiped: %#v", got.SubscriptionPlanWeights)
	}
	// reset to builtin
	got, err = m.UpdateSupplier("anthropic", SupplierConfigurable{
		Name:                    b.Name,
		Models:                  append([]string(nil), b.Models...),
		SubscriptionPlanWeights: BuiltinSubscriptionPlanWeights("anthropic"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.data["anthropic"]; ok {
		t.Fatal("overlay should clear when all defaults")
	}
	if m.UsageWeight("anthropic", PlanClaudeMax) != 20 {
		t.Fatal("reset weight")
	}
	if _, err := m.UpdateSupplier("anthropic", SupplierConfigurable{
		Name:                    b.Name,
		Models:                  append([]string(nil), b.Models...),
		SubscriptionPlanWeights: map[string]int{"claude_free": 1},
	}); err == nil {
		t.Fatal("expected invalid plan key")
	}
	if _, err := m.UpdateSupplier("deepseek", SupplierConfigurable{
		Name:                    "Deepseek",
		Models:                  []string{"x"},
		SubscriptionPlanWeights: map[string]int{PlanClaudePro: 1},
	}); err == nil {
		t.Fatal("deepseek must reject plan weights")
	}
}

func TestMergeSupplierIncludesBuiltinWeights(t *testing.T) {
	b, _ := builtinByID("openai")
	s := MergeSupplier(b, SupplierOverlay{})
	if s.SubscriptionPlanWeights[PlanCodexPro20x] != 20 {
		t.Fatalf("%#v", s.SubscriptionPlanWeights)
	}
}
