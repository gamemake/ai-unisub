package aiprovider

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSupplierMappingIsolationAndDefaults(t *testing.T) {
	m := routingManager(t)
	c := m.Catalog()
	for _, supplier := range BuiltinSuppliers() {
		if err := ValidateCatalog(Catalog{Suppliers: BuiltinSuppliers()}); err != nil {
			t.Fatal(err)
		}
		for _, client := range []ClientType{ClientAnthropic, ClientOpenAI, ClientGrok} {
			found := false
			for _, mapping := range supplier.Mappings {
				if mapping.Client == client {
					found = true
					if got := m.MapModel(client, supplier.ID, mapping.Model); got != mapping.Target {
						t.Fatalf("%s %s %s = %s", supplier.ID, client, mapping.Model, got)
					}
				}
			}
			native := supplier.ID == "anthropic" && client == ClientAnthropic || supplier.ID == "openai" && client == ClientOpenAI || supplier.ID == "grok" && client == ClientGrok
			if found == native {
				t.Fatalf("%s %s: native models must pass through, other clients need mappings", supplier.ID, client)
			}
		}
	}
	for i := range c.Suppliers {
		c.Suppliers[i].Mappings = []ModelMapping{{Client: ClientOpenAI, Model: "custom", Target: c.Suppliers[i].ID}}
	}
	if err := m.SetCatalog(c); err != nil {
		t.Fatal(err)
	}
	c.Suppliers[0].Mappings[0].Target = "mutated"
	if got := m.MapModel(ClientOpenAI, "anthropic", "custom"); got != "anthropic" {
		t.Fatal("save aliases caller", got)
	}
	out := m.Catalog()
	out.Suppliers[0].Mappings[0].Target = "mutated"
	for _, supplier := range c.Suppliers {
		if got := m.MapModel(ClientOpenAI, supplier.ID, "custom"); got != supplier.ID {
			t.Fatal("cross-supplier leak", got)
		}
		if got := m.MapModel(ClientGrok, supplier.ID, "custom"); got != "custom" {
			t.Fatal("cross-client leak", got)
		}
		if got := m.MapModel(ClientOpenAI, supplier.ID, "unknown-future-model"); got != "unknown-future-model" {
			t.Fatal("unknown model rewritten")
		}
	}
	out = m.Catalog()
	out.Suppliers[0].Mappings = append(out.Suppliers[0].Mappings, out.Suppliers[0].Mappings[0])
	if m.SetCatalog(out) == nil {
		t.Fatal("duplicate mapping accepted")
	}
}

func TestSavedGlobalCatalogOverridesAreKeptWithinSupplier(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"suppliers": SupplierConfigs(), "mappings": []map[string]string{{"supplier": "kimi", "client": "OpenAI", "model": "local", "target": "private-model"}}})
	var c Catalog
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCatalog(c); err != nil {
		t.Fatal(err)
	}
	if len(c.Suppliers[5].Mappings) != 1 || c.Suppliers[5].Mappings[0].Target != "private-model" {
		t.Fatal("lost saved override")
	}
	out, _ := json.Marshal(c)
	var keys map[string]json.RawMessage
	_ = json.Unmarshal(out, &keys)
	if _, ok := keys["mappings"]; ok {
		t.Fatal("still writes global mappings")
	}
}

func TestClientTypeIsSingleValueAndIgnoresOldField(t *testing.T) {
	for _, raw := range []string{`{}`, `{"client_types":["OpenAI","Grok"]}`, `{"client_types":{"invalid":"ignored"}}`} {
		c, err := decodeAIProviderConfig("a", json.RawMessage(raw))
		if err != nil || c.ClientType != ClientAny {
			t.Fatalf("default/old field: %s %+v %v", raw, c, err)
		}
	}
	for _, raw := range []string{`{"client_type":["OpenAI"]}`, `{"client_type":"Other"}`} {
		if _, err := decodeAIProviderConfig("a", json.RawMessage(raw)); err == nil {
			t.Fatal("accepted non-scalar/invalid client type")
		}
	}
	raw, err := prepareConfig("a", "dummy", json.RawMessage(`{"client_type":"Grok","client_types":["OpenAI"]}`))
	if err != nil || strings.Contains(string(raw), "client_types") {
		t.Fatalf("old array retained: %s %v", raw, err)
	}
	c, err := decodeAIProviderConfig("a", raw)
	if err != nil || !AllowsClient(c, ClientGrok) || AllowsClient(c, ClientOpenAI) || AllowsClient(c, "") {
		t.Fatalf("single policy not applied: %+v %v", c, err)
	}
}
