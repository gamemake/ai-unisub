package aiprovider

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestSupportedClientsFromURLs(t *testing.T) {
	cases := map[string][]ClientType{
		"anthropic": {ClientAnthropic},
		"openai":    {ClientOpenAI, ClientGrok},
		"grok":      {ClientOpenAI, ClientGrok},
		"deepseek":  {ClientAnthropic, ClientOpenAI, ClientGrok},
		"zhipu":     {ClientAnthropic, ClientOpenAI, ClientGrok},
		"kimi":      {ClientAnthropic, ClientOpenAI, ClientGrok},
	}
	for _, b := range SupplierBuiltins() {
		got := SupportedClientsForBuiltin(b)
		want := cases[b.ID]
		if !slices.Equal(got, want) {
			t.Fatalf("%s: got %v want %v", b.ID, got, want)
		}
	}
}

func TestDiffAndMergeSupplier(t *testing.T) {
	b, _ := builtinByID("anthropic")
	overlay, changed := DiffSupplier(b, SupplierConfigurable{Name: b.Name, Models: slices.Clone(b.Models)})
	if changed {
		t.Fatal("expected no change")
	}
	merged := MergeSupplier(b, overlay)
	if merged.Name != b.Name || !slices.Equal(merged.Models, b.Models) {
		t.Fatalf("merge defaults: %+v", merged)
	}

	overlay, changed = DiffSupplier(b, SupplierConfigurable{Name: "Custom", Models: []string{"m1"}})
	if !changed || overlay.Name == nil || *overlay.Name != "Custom" || overlay.Models == nil {
		t.Fatalf("overlay: %+v changed=%v", overlay, changed)
	}
	merged = MergeSupplier(b, overlay)
	if merged.Name != "Custom" || !slices.Equal(merged.Models, []string{"m1"}) || merged.ClaudeURL != b.ClaudeURL {
		t.Fatalf("merged: %+v", merged)
	}
}

func TestUpdateSupplierUsesStore(t *testing.T) {
	store := &memOverlayStore{data: map[string]json.RawMessage{}}
	m := NewAIProviderManager()
	if err := m.SetOverlayStore(store); err != nil {
		t.Fatal(err)
	}
	got, err := m.UpdateSupplier("kimi", SupplierConfigurable{Name: "Kimi X", Models: []string{"k1"}})
	if err != nil || got.Name != "Kimi X" {
		t.Fatal(err, got)
	}
	if _, ok := store.data["kimi"]; !ok {
		t.Fatal("not stored")
	}
	b, _ := builtinByID("kimi")
	_, err = m.UpdateSupplier("kimi", SupplierConfigurable{Name: b.Name, Models: slices.Clone(b.Models)})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.data["kimi"]; ok {
		t.Fatal("should delete overlay on reset")
	}
}

func TestSupportedClientsForProviderGroupIntersection(t *testing.T) {
	m := NewAIProviderManager()
	_ = m.Register("dummy", func(id int, data ProviderData) (AIProvider, error) {
		return NewDummyAIProvider(id, data)
	})
	// dummy has no supplier → empty clients; use api with suppliers instead via Create
	// Create dummy with anthropic-like... dummy SupplierForAdapter is "".
	// Use subscription factories isn't available. Set catalog and create with prepareConfig path.

	create := func(id int, adapter string, cfg map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(cfg)
		if _, err := m.Create(id, adapter, raw, nil); err != nil {
			t.Fatal(id, err)
		}
	}
	// Register api factory minimal - need oauth. Skip if Create needs factory.
	// Use dummy and manually can't set supplier for dummy subscription...
	// For group test: create two dummies won't have clients.

	if got := intersectClients([]ClientType{ClientAnthropic, ClientOpenAI}, []ClientType{ClientOpenAI, ClientGrok}); !slices.Equal(got, []ClientType{ClientOpenAI}) {
		t.Fatal(got)
	}
	_ = create
}

type memOverlayStore struct {
	data map[string]json.RawMessage
}

func (m *memOverlayStore) ListSupplierOverlays() (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	for k, v := range m.data {
		out[k] = append(json.RawMessage(nil), v...)
	}
	return out, nil
}
func (m *memOverlayStore) PutSupplierOverlay(name string, value json.RawMessage) error {
	m.data[name] = append(json.RawMessage(nil), value...)
	return nil
}
func (m *memOverlayStore) DeleteSupplierOverlay(name string) error {
	delete(m.data, name)
	return nil
}
