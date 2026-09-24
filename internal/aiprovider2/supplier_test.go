package aiprovider2

import (
	"slices"
	"testing"
)

func TestMapModelSupportsMultipleWildcards(t *testing.T) {
	mappings := []ModelMapping{
		{Pattern: "claude-*-*-latest", Target: "glm-4.5"},
		{Pattern: "*", Target: "fallback"},
	}

	if got := MapModel(mappings, "claude-sonnet-4-latest"); got != "glm-4.5" {
		t.Fatalf("MapModel() = %q, want %q", got, "glm-4.5")
	}
	if got := MapModel(mappings, "other"); got != "fallback" {
		t.Fatalf("MapModel() fallback = %q, want %q", got, "fallback")
	}
	if got := MapModel(nil, "unchanged"); got != "unchanged" {
		t.Fatalf("MapModel() passthrough = %q", got)
	}
}

func TestSupplierMappingsCanBeOverlaidAndMustTargetModel(t *testing.T) {
	supplier := &SupplierData{
		id: "test",
		builtin: SupplierBuiltinConfig{
			Models:   []string{"model-a", "model-b"},
			Mappings: []ModelMapping{{Pattern: "builtin", Target: "model-a"}},
		},
	}
	overlay := SupplierOverlayConfig{
		Mappings: []ModelMapping{{Pattern: "client-*-*", Target: "model-b"}},
	}
	if err := supplier.SetOverlayConfig(overlay, false); err != nil {
		t.Fatal(err)
	}
	if got := supplier.GetConfig().Mappings; !slices.Equal(got, overlay.Mappings) {
		t.Fatalf("effective mappings = %+v, want %+v", got, overlay.Mappings)
	}
	if err := supplier.SetOverlayConfig(SupplierOverlayConfig{
		Mappings: []ModelMapping{{Pattern: "bad", Target: "missing"}},
	}, false); err == nil {
		t.Fatal("SetOverlayConfig() accepted a mapping to an unknown model")
	}
	if got := supplier.GetConfig().Mappings; !slices.Equal(got, overlay.Mappings) {
		t.Fatalf("invalid overlay changed effective mappings: %+v", got)
	}

	config := supplier.GetBuiltinConfig()
	config.Mappings = []ModelMapping{}
	diff, err := supplier.DiffConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if diff.Mappings == nil || len(diff.Mappings) != 0 {
		t.Fatalf("empty mapping override was not preserved: %#v", diff.Mappings)
	}
}

func TestAccountGroupGetModelsIntersectsAfterMapping(t *testing.T) {
	manager := &Manager{
		accounts:     make(map[int]*Account),
		suppliersMap: make(map[string]Supplier),
	}
	left := newSupplierZAI(manager)
	right := newSupplierDeepSeek(manager)
	if err := left.SetOverlayConfig(SupplierOverlayConfig{
		Mappings: []ModelMapping{{Pattern: "shared", Target: "glm-4.5"}},
	}, false); err != nil {
		t.Fatal(err)
	}
	if err := right.SetOverlayConfig(SupplierOverlayConfig{
		Mappings: []ModelMapping{{Pattern: "shared", Target: "deepseek-chat"}},
	}, false); err != nil {
		t.Fatal(err)
	}
	manager.suppliersMap[left.GetID()] = left
	manager.suppliersMap[right.GetID()] = right
	manager.accounts[1] = &Account{Manager: manager, ID: 1, Config: AccountConfig{Kind: AccountAPI, Supplier: left.GetID()}}
	manager.accounts[2] = &Account{Manager: manager, ID: 2, Config: AccountConfig{Kind: AccountAPI, Supplier: right.GetID()}}
	group := &Account{
		Manager: manager,
		ID:      3,
		Config: AccountConfig{
			Kind:    AccountGroup,
			Members: []GroupMember{{ID: 1, Weight: 1}, {ID: 2, Weight: 1}},
		},
	}

	if got := group.GetModels(); !slices.Equal(got, []string{"shared"}) {
		t.Fatalf("GetModels() = %v, want [shared]", got)
	}
}
