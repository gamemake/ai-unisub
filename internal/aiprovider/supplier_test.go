package aiprovider

import (
	"errors"
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
	manager := &providerManager{
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
	manager.accounts[1] = &Account{manager: manager, ID: 1, Config: AccountConfig{Kind: AccountAPI, Supplier: left.GetID()}}
	manager.accounts[2] = &Account{manager: manager, ID: 2, Config: AccountConfig{Kind: AccountAPI, Supplier: right.GetID()}}
	group := &Account{
		manager: manager,
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

func TestAccountSupportedClients(t *testing.T) {
	manager := &providerManager{
		accounts:     make(map[int]*Account),
		suppliersMap: make(map[string]Supplier),
	}
	for _, supplier := range []Supplier{newSupplierAnthropic(manager), newSupplierOpenAI(manager), newSupplierDeepSeek(manager), newSupplierDummy(manager)} {
		manager.suppliersMap[supplier.GetID()] = supplier
	}
	add := func(id int, config AccountConfig) *Account {
		account := &Account{manager: manager, ID: id, Config: config}
		manager.accounts[id] = account
		return account
	}
	anthropic := add(1, AccountConfig{Kind: AccountSubscription, Supplier: "anthropic"})
	openai := add(2, AccountConfig{Kind: AccountSubscription, Supplier: "openai"})
	deepseek := add(3, AccountConfig{Kind: AccountAPI, Supplier: "deepseek"})
	restricted := add(4, AccountConfig{Kind: AccountAPI, Supplier: "deepseek", ClientType: ClientCodex})
	group := add(5, AccountConfig{Kind: AccountGroup, Members: []GroupMember{{ID: 3, Weight: 1}, {ID: 2, Weight: 1}}})
	dummy := add(7, AccountConfig{Kind: AccountSubscription, Supplier: "dummy"})
	disjoint := add(6, AccountConfig{Kind: AccountGroup, Members: []GroupMember{{ID: 1, Weight: 1}, {ID: 2, Weight: 1}}})

	for _, tc := range []struct {
		name    string
		account *Account
		want    []ClientType
	}{
		{"anthropic", anthropic, []ClientType{ClientClaude}},
		{"openai", openai, []ClientType{ClientCodex, ClientGrok}},
		{"deepseek", deepseek, []ClientType{ClientClaude, ClientCodex, ClientGrok}},
		{"dummy", dummy, []ClientType{ClientClaude}},
		{"client_type", restricted, []ClientType{ClientCodex}},
		{"group", group, []ClientType{ClientCodex, ClientGrok}},
	} {
		got, err := tc.account.SupportedClients()
		if err != nil {
			t.Errorf("%s: SupportedClients() error = %v", tc.name, err)
			continue
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: SupportedClients() = %v, want %v", tc.name, got, tc.want)
		}
	}
	if got, err := disjoint.SupportedClients(); got != nil || !errors.Is(err, errNoSupportedClients) {
		t.Errorf("disjoint group: SupportedClients() = %v, %v, want nil, %v", got, err, errNoSupportedClients)
	}
}
