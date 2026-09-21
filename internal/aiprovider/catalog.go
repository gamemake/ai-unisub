package aiprovider

import (
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"
)

// ModuleConfigKey is the legacy whole-catalog module config document.
// New writes use per-supplier PersistedConfig rows (SupplierConfigType).
const ModuleConfigKey = "aiprovider"

// SupplierConfigType is the PersistedConfig.type value for supplier overlays.
const SupplierConfigType = "supplier"

// Supplier is the runtime / API view of one model supplier.
type Supplier struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	ClaudeURL string   `json:"claude_url"`
	OpenAIURL string   `json:"openai_url"`
	Models    []string `json:"models"`
	// ModelMappings rewrites client model names before upstream forward.
	// Empty means passthrough. First matching rule wins; From supports one '*'.
	ModelMappings []ModelMapping `json:"model_mappings,omitempty"`
	// SupportedClients is derived from non-empty URLs (code-owned).
	SupportedClients []ClientType `json:"supported_clients"`
	// SubscriptionPlanWeights is a flat plan_id → usage weight map for
	// future same-tier load balancing. Code defaults; overlay may replace.
	SubscriptionPlanWeights map[string]int `json:"subscription_plan_weights,omitempty"`
	// Only non-authentication header overrides are persisted. Request defaults
	// and endpoint behavior are code-owned; credentials are injected externally.
	SubscriptionUsageHeaderOverrides map[string]string `json:"subscription_usage_header_overrides,omitempty"`
	APIUsageHeaderOverrides          map[string]string `json:"api_usage_header_overrides,omitempty"`
}

// CodexURL is a compatibility alias for OpenAIURL in older call sites.
func (s Supplier) CodexURL() string { return s.OpenAIURL }

type Catalog struct {
	Suppliers []Supplier `json:"suppliers"`
}

// SupplierOverlay holds only fields that differ from SupplierBuiltin.
// Nil pointer / nil slice pointer means "not overridden".
type SupplierOverlay struct {
	Name                             *string           `json:"name,omitempty"`
	Models                           *[]string         `json:"models,omitempty"`
	ModelMappings                    *[]ModelMapping   `json:"model_mappings,omitempty"`
	SubscriptionPlanWeights          map[string]int    `json:"subscription_plan_weights,omitempty"`
	SubscriptionUsageHeaderOverrides map[string]string `json:"subscription_usage_header_overrides,omitempty"`
	APIUsageHeaderOverrides          map[string]string `json:"api_usage_header_overrides,omitempty"`
}

// SupplierConfigurable is the desired effective configurable state for a PUT.
type SupplierConfigurable struct {
	Name                             string            `json:"name"`
	Models                           []string          `json:"models"`
	ModelMappings                    []ModelMapping    `json:"model_mappings"`
	SubscriptionPlanWeights          map[string]int    `json:"subscription_plan_weights,omitempty"`
	SubscriptionUsageHeaderOverrides map[string]string `json:"subscription_usage_header_overrides,omitempty"`
	APIUsageHeaderOverrides          map[string]string `json:"api_usage_header_overrides,omitempty"`
}

// SupplierOverlayStore persists per-supplier overlays without importing database.
type SupplierOverlayStore interface {
	ListSupplierOverlays() (map[string]json.RawMessage, error)
	PutSupplierOverlay(name string, value json.RawMessage) error
	DeleteSupplierOverlay(name string) error
}

// SupportedClientsForBuiltin derives client capabilities from built-in URLs.
// Claude URL → Anthropic (Claude Code); OpenAI-compatible URL → OpenAI (Codex) and Grok.
func SupportedClientsForBuiltin(b SupplierBuiltin) []ClientType {
	var out []ClientType
	if b.ClaudeURL != "" {
		out = append(out, ClientClaude)
	}
	if b.OpenAIURL != "" {
		out = append(out, ClientCodex, ClientGrok)
	}
	return out
}

// MergeSupplier applies an overlay onto code defaults.
func MergeSupplier(b SupplierBuiltin, o SupplierOverlay) Supplier {
	s := Supplier{
		ID:                      b.ID,
		Name:                    b.Name,
		ClaudeURL:               b.ClaudeURL,
		OpenAIURL:               b.OpenAIURL,
		Models:                  slices.Clone(b.Models),
		SupportedClients:        SupportedClientsForBuiltin(b),
		SubscriptionPlanWeights: maps.Clone(BuiltinSubscriptionPlanWeights(b.ID)),
	}
	if o.Name != nil {
		s.Name = *o.Name
	}
	if o.Models != nil {
		s.Models = slices.Clone(*o.Models)
	}
	if o.ModelMappings != nil {
		s.ModelMappings = cloneModelMappings(*o.ModelMappings)
	}
	if len(o.SubscriptionPlanWeights) > 0 {
		// Overlay stores the full effective map when any weight differs.
		s.SubscriptionPlanWeights = maps.Clone(o.SubscriptionPlanWeights)
	}
	if len(o.SubscriptionUsageHeaderOverrides) > 0 {
		s.SubscriptionUsageHeaderOverrides = maps.Clone(o.SubscriptionUsageHeaderOverrides)
	}
	if len(o.APIUsageHeaderOverrides) > 0 {
		s.APIUsageHeaderOverrides = maps.Clone(o.APIUsageHeaderOverrides)
	}
	return s
}

// DiffSupplier returns the overlay of desired vs builtin. changed is false when
// desired matches builtin completely (caller should delete any stored row).
func DiffSupplier(b SupplierBuiltin, desired SupplierConfigurable) (SupplierOverlay, bool) {
	var o SupplierOverlay
	name := strings.TrimSpace(desired.Name)
	if name != "" && name != b.Name {
		n := name
		o.Name = &n
	}
	if !sameStringSlice(desired.Models, b.Models) {
		models := slices.Clone(desired.Models)
		if models == nil {
			models = []string{}
		}
		o.Models = &models
	}
	// Builtin has no mappings; any non-empty list is an overlay. Empty desired
	// matches builtin (no overlay field).
	if len(desired.ModelMappings) > 0 {
		mappings := cloneModelMappings(desired.ModelMappings)
		o.ModelMappings = &mappings
	}
	if !sameStringMap(desired.SubscriptionUsageHeaderOverrides, nil) {
		// empty desired map means clear override (no overlay field)
		if len(desired.SubscriptionUsageHeaderOverrides) > 0 {
			o.SubscriptionUsageHeaderOverrides = maps.Clone(desired.SubscriptionUsageHeaderOverrides)
		}
	}
	if !sameStringMap(desired.APIUsageHeaderOverrides, nil) {
		if len(desired.APIUsageHeaderOverrides) > 0 {
			o.APIUsageHeaderOverrides = maps.Clone(desired.APIUsageHeaderOverrides)
		}
	}
	// nil desired weights means "leave unchanged" at the call site; callers that
	// rebuild a full overlay should pass the current effective map explicitly.
	if desired.SubscriptionPlanWeights != nil {
		if weights, err := diffSubscriptionPlanWeights(b.ID, desired.SubscriptionPlanWeights); err == nil && len(weights) > 0 {
			o.SubscriptionPlanWeights = weights
		}
	}
	changed := o.Name != nil || o.Models != nil || o.ModelMappings != nil || len(o.SubscriptionPlanWeights) > 0 || len(o.SubscriptionUsageHeaderOverrides) > 0 || len(o.APIUsageHeaderOverrides) > 0
	return o, changed
}

func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameStringMap(a, b map[string]string) bool {
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

func ParseSupplierOverlay(raw json.RawMessage) (SupplierOverlay, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return SupplierOverlay{}, nil
	}
	var o SupplierOverlay
	if err := json.Unmarshal(raw, &o); err != nil {
		return SupplierOverlay{}, err
	}
	return o, nil
}

func ValidateSupplierOverlay(o SupplierOverlay) error {
	return ValidateSupplierOverlayFor("", o)
}

// ValidateSupplierOverlayFor checks overlay fields; supplierID enables plan-weight key checks.
func ValidateSupplierOverlayFor(supplierID string, o SupplierOverlay) error {
	if o.Name != nil {
		n := strings.TrimSpace(*o.Name)
		if n == "" || n != *o.Name {
			return errors.New("supplier name is required and must not have surrounding whitespace")
		}
	}
	if !validQuotaHeaders(o.SubscriptionUsageHeaderOverrides) || !validQuotaHeaders(o.APIUsageHeaderOverrides) {
		return errors.New("usage header overrides contain invalid or authentication headers")
	}
	if o.Models != nil {
		for _, m := range *o.Models {
			if strings.TrimSpace(m) == "" || strings.TrimSpace(m) != m {
				return errors.New("model names must be non-empty without surrounding whitespace")
			}
		}
	}
	if o.ModelMappings != nil {
		if err := validateModelMappings(*o.ModelMappings); err != nil {
			return err
		}
	}
	if len(o.SubscriptionPlanWeights) > 0 {
		id := strings.TrimSpace(supplierID)
		if id == "" {
			// Without id, only check value floor; key set validated on apply.
			for _, w := range o.SubscriptionPlanWeights {
				if w < 1 {
					return errors.New("subscription plan weight must be at least 1")
				}
			}
		} else if err := validateSubscriptionPlanWeights(id, o.SubscriptionPlanWeights); err != nil {
			return err
		}
	}
	return nil
}

func ValidateCatalog(c Catalog) error {
	known := map[string]SupplierBuiltin{}
	for _, b := range SupplierBuiltins() {
		known[b.ID] = b
	}
	seen := map[string]bool{}
	for _, s := range c.Suppliers {
		if !validQuotaHeaders(s.SubscriptionUsageHeaderOverrides) || !validQuotaHeaders(s.APIUsageHeaderOverrides) {
			return errors.New("usage header overrides contain invalid or authentication headers")
		}
		b, ok := known[s.ID]
		if !ok || seen[s.ID] || strings.TrimSpace(s.Name) == "" {
			return errors.New("invalid or duplicate supplier")
		}
		if s.ClaudeURL != b.ClaudeURL || s.OpenAIURL != b.OpenAIURL {
			return errors.New("built-in supplier URLs cannot be modified")
		}
		if err := validateSubscriptionPlanWeights(s.ID, s.SubscriptionPlanWeights); err != nil {
			return err
		}
		seen[s.ID] = true
	}
	if len(seen) != len(known) {
		return errors.New("built-in suppliers cannot be deleted")
	}
	return nil
}

func (m *AIProviderManager) Catalog() Catalog {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneCatalog(m.catalog)
}

func (m *AIProviderManager) Supplier(id string) (Supplier, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.catalog.Suppliers {
		if s.ID == id {
			return cloneSupplier(s), true
		}
	}
	return Supplier{}, false
}

// SupplierIDForAccount resolves the model-supplier catalog id for an account
// that will actually execute a request (member after group selection).
func SupplierIDForAccount(config AIProviderConfig, adapter string) string {
	if id := strings.TrimSpace(config.Supplier); id != "" {
		return id
	}
	return SupplierForAdapter(adapter)
}

// ApplyModelMappings rewrites request model names using the supplier catalog
// entry for supplierID. Missing suppliers or empty rules leave body unchanged.
func (m *AIProviderManager) ApplyModelMappings(supplierID string, body []byte, headers http.Header) []byte {
	if m == nil || supplierID == "" || len(body) == 0 && headers == nil {
		return body
	}
	s, ok := m.Supplier(supplierID)
	if !ok || len(s.ModelMappings) == 0 {
		return body
	}
	return RewriteRequestModel(body, headers, s.ModelMappings)
}

func cloneCatalog(c Catalog) Catalog {
	c.Suppliers = slices.Clone(c.Suppliers)
	for i := range c.Suppliers {
		c.Suppliers[i] = cloneSupplier(c.Suppliers[i])
	}
	return c
}

func cloneSupplier(s Supplier) Supplier {
	s.Models = slices.Clone(s.Models)
	s.ModelMappings = cloneModelMappings(s.ModelMappings)
	s.SupportedClients = slices.Clone(s.SupportedClients)
	s.SubscriptionPlanWeights = maps.Clone(s.SubscriptionPlanWeights)
	s.SubscriptionUsageHeaderOverrides = maps.Clone(s.SubscriptionUsageHeaderOverrides)
	s.APIUsageHeaderOverrides = maps.Clone(s.APIUsageHeaderOverrides)
	return s
}

// SetCatalog replaces the in-memory catalog. Prefer LoadOverlays / UpdateSupplier
// for persistence-backed updates.
func (m *AIProviderManager) SetCatalog(c Catalog) error {
	if err := ValidateCatalog(c); err != nil {
		return err
	}
	// Ensure derived fields are filled even if callers omit them.
	for i := range c.Suppliers {
		if b, ok := builtinByID(c.Suppliers[i].ID); ok {
			c.Suppliers[i].ClaudeURL = b.ClaudeURL
			c.Suppliers[i].OpenAIURL = b.OpenAIURL
			if c.Suppliers[i].SupportedClients == nil {
				c.Suppliers[i].SupportedClients = SupportedClientsForBuiltin(b)
			}
			if c.Suppliers[i].Models == nil {
				c.Suppliers[i].Models = slices.Clone(b.Models)
			}
			if c.Suppliers[i].SubscriptionPlanWeights == nil {
				c.Suppliers[i].SubscriptionPlanWeights = maps.Clone(BuiltinSubscriptionPlanWeights(b.ID))
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.catalog = cloneCatalog(c)
	m.invalidateQuotaCachesLocked()
	return nil
}

func (m *AIProviderManager) invalidateQuotaCachesLocked() {
	for _, provider := range m.aiProviders {
		if cached, ok := provider.(interface{ invalidateQuotaCache() }); ok {
			cached.invalidateQuotaCache()
		}
	}
}

// SetOverlayStore registers persistence for supplier overlays and reloads catalog.
func (m *AIProviderManager) SetOverlayStore(store SupplierOverlayStore) error {
	m.mu.Lock()
	m.overlayStore = store
	m.mu.Unlock()
	if store == nil {
		return m.SetCatalog(Catalog{Suppliers: SupplierConfigs()})
	}
	overlays, err := store.ListSupplierOverlays()
	if err != nil {
		return err
	}
	return m.applyOverlays(overlays)
}

// LoadOverlays rebuilds the catalog from builtins plus stored overlays.
func (m *AIProviderManager) LoadOverlays(overlays map[string]json.RawMessage) error {
	return m.applyOverlays(overlays)
}

func (m *AIProviderManager) applyOverlays(overlays map[string]json.RawMessage) error {
	suppliers := make([]Supplier, 0, len(SupplierBuiltins()))
	for _, b := range SupplierBuiltins() {
		var o SupplierOverlay
		if raw, ok := overlays[b.ID]; ok {
			parsed, err := ParseSupplierOverlay(raw)
			if err != nil {
				return err
			}
			if err := ValidateSupplierOverlayFor(b.ID, parsed); err != nil {
				return err
			}
			o = parsed
		}
		suppliers = append(suppliers, MergeSupplier(b, o))
	}
	return m.SetCatalog(Catalog{Suppliers: suppliers})
}

// UpdateSupplier applies a desired configurable state for one supplier.
// Diff runs here; the store receives only the overlay JSON or a delete.
func (m *AIProviderManager) UpdateSupplier(id string, desired SupplierConfigurable) (Supplier, error) {
	b, ok := builtinByID(id)
	if !ok {
		return Supplier{}, errors.New("unknown supplier")
	}
	desired.Name = strings.TrimSpace(desired.Name)
	if desired.Name == "" {
		return Supplier{}, errors.New("supplier name is required")
	}
	if desired.Models == nil {
		desired.Models = []string{}
	}
	for _, model := range desired.Models {
		if strings.TrimSpace(model) == "" || strings.TrimSpace(model) != model {
			return Supplier{}, errors.New("model names must be non-empty without surrounding whitespace")
		}
	}
	if desired.ModelMappings == nil {
		desired.ModelMappings = []ModelMapping{}
	}
	if err := validateModelMappings(desired.ModelMappings); err != nil {
		return Supplier{}, err
	}
	if !validQuotaHeaders(desired.SubscriptionUsageHeaderOverrides) || !validQuotaHeaders(desired.APIUsageHeaderOverrides) {
		return Supplier{}, errors.New("usage header overrides contain invalid or authentication headers")
	}
	m.mu.RLock()
	store := m.overlayStore
	var currentWeights map[string]int
	for _, s := range m.catalog.Suppliers {
		if s.ID == id {
			currentWeights = maps.Clone(s.SubscriptionPlanWeights)
			break
		}
	}
	m.mu.RUnlock()
	if desired.SubscriptionPlanWeights == nil {
		// Preserve effective weights on name/models-only updates.
		desired.SubscriptionPlanWeights = currentWeights
	} else if _, err := normalizeSubscriptionPlanWeights(id, desired.SubscriptionPlanWeights); err != nil {
		return Supplier{}, err
	}
	overlay, changed := DiffSupplier(b, desired)
	if err := ValidateSupplierOverlayFor(id, overlay); err != nil {
		return Supplier{}, err
	}
	if store != nil {
		if !changed {
			if err := store.DeleteSupplierOverlay(id); err != nil {
				return Supplier{}, err
			}
		} else {
			raw, err := json.Marshal(overlay)
			if err != nil {
				return Supplier{}, err
			}
			if err := store.PutSupplierOverlay(id, raw); err != nil {
				return Supplier{}, err
			}
		}
	}

	merged := MergeSupplier(b, overlay)
	m.mu.Lock()
	defer m.mu.Unlock()
	found := false
	for i := range m.catalog.Suppliers {
		if m.catalog.Suppliers[i].ID == id {
			m.catalog.Suppliers[i] = cloneSupplier(merged)
			found = true
			break
		}
	}
	if !found {
		m.catalog.Suppliers = append(m.catalog.Suppliers, cloneSupplier(merged))
	}
	m.invalidateQuotaCachesLocked()
	return cloneSupplier(merged), nil
}

func (m *AIProviderManager) DefaultURL(supplier string, client ClientType) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.catalog.Suppliers {
		if s.ID == supplier {
			return s.URLForClient(client)
		}
	}
	return ""
}

// URLForClient uses the shared OpenAI-compatible endpoint for Grok.
func (s Supplier) URLForClient(client ClientType) string {
	if client == ClientClaude {
		return s.ClaudeURL
	}
	return s.OpenAIURL
}

// RestoreBuiltinURLs forces code-owned URLs onto a catalog loaded from storage.
func RestoreBuiltinURLs(c *Catalog) {
	for i := range c.Suppliers {
		if b, ok := builtinByID(c.Suppliers[i].ID); ok {
			c.Suppliers[i].ClaudeURL = b.ClaudeURL
			c.Suppliers[i].OpenAIURL = b.OpenAIURL
			if c.Suppliers[i].SupportedClients == nil {
				c.Suppliers[i].SupportedClients = SupportedClientsForBuiltin(b)
			}
			if c.Suppliers[i].Models == nil {
				c.Suppliers[i].Models = slices.Clone(b.Models)
			}
			if c.Suppliers[i].SubscriptionPlanWeights == nil {
				c.Suppliers[i].SubscriptionPlanWeights = maps.Clone(BuiltinSubscriptionPlanWeights(b.ID))
			}
		}
	}
}

// MigrateLegacyCatalogOverlays extracts per-supplier overlays from a legacy
// module_configs catalog document. Only fields that differ from builtins.
func MigrateLegacyCatalogOverlays(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var c Catalog
	// Accept legacy codex_url by decoding into a flexible shape first.
	var wire struct {
		Suppliers []struct {
			ID                               string            `json:"id"`
			Name                             string            `json:"name"`
			ClaudeURL                        string            `json:"claude_url"`
			OpenAIURL                        string            `json:"openai_url"`
			CodexURL                         string            `json:"codex_url"`
			Models                           []string          `json:"models"`
			ModelMappings                    []ModelMapping    `json:"model_mappings"`
			SubscriptionUsageHeaderOverrides map[string]string `json:"subscription_usage_header_overrides"`
			APIUsageHeaderOverrides          map[string]string `json:"api_usage_header_overrides"`
		} `json:"suppliers"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, err
	}
	_ = c
	out := map[string]json.RawMessage{}
	for _, item := range wire.Suppliers {
		b, ok := builtinByID(item.ID)
		if !ok {
			continue
		}
		desired := SupplierConfigurable{
			Name:                             item.Name,
			Models:                           item.Models,
			ModelMappings:                    item.ModelMappings,
			SubscriptionUsageHeaderOverrides: item.SubscriptionUsageHeaderOverrides,
			APIUsageHeaderOverrides:          item.APIUsageHeaderOverrides,
		}
		if desired.Name == "" {
			desired.Name = b.Name
		}
		if desired.Models == nil {
			desired.Models = slices.Clone(b.Models)
		}
		overlay, changed := DiffSupplier(b, desired)
		if !changed {
			continue
		}
		rawOverlay, err := json.Marshal(overlay)
		if err != nil {
			return nil, err
		}
		out[item.ID] = rawOverlay
	}
	return out, nil
}

// EndpointClient follows the wire protocol, including requests from generic SDKs.
func EndpointClient(path string) ClientType {
	if strings.HasSuffix(path, "/messages") || strings.HasSuffix(path, "/messages/count_tokens") {
		return ClientClaude
	}
	return ClientCodex
}

// SupportedClientsForProvider returns protocol capability clients for one
// account (for CC Switch / Key DTO). Derived only from the bound supplier's
// URLs (or the intersection of group members). Provider client_type policy is
// not applied here — that gate remains on the gateway request path.
func (m *AIProviderManager) SupportedClientsForProvider(id int) []ClientType {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.supportedClientsLocked(id, map[int]bool{})
}

func (m *AIProviderManager) supportedClientsLocked(id int, stack map[int]bool) []ClientType {
	if stack[id] {
		return nil
	}
	p, ok := m.aiProviders[id]
	if !ok {
		return nil
	}
	cfg := p.Config()
	if cfg.Kind == "group" {
		stack[id] = true
		var acc []ClientType
		first := true
		for _, member := range cfg.Members {
			part := m.supportedClientsLocked(member.ID, stack)
			if first {
				acc = slices.Clone(part)
				first = false
				continue
			}
			acc = intersectClients(acc, part)
		}
		delete(stack, id)
		return acc
	}
	return m.supplierClientsLocked(cfg, m.adapters[id])
}

func (m *AIProviderManager) supplierClientsLocked(cfg AIProviderConfig, adapter string) []ClientType {
	supplierID := cfg.Supplier
	if supplierID == "" {
		supplierID = SupplierForAdapter(adapter)
	}
	if supplierID == "" {
		return nil
	}
	for _, s := range m.catalog.Suppliers {
		if s.ID == supplierID {
			return slices.Clone(s.SupportedClients)
		}
	}
	if b, ok := builtinByID(supplierID); ok {
		return SupportedClientsForBuiltin(b)
	}
	return nil
}

func intersectClients(a, b []ClientType) []ClientType {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	set := map[ClientType]bool{}
	for _, c := range b {
		set[c] = true
	}
	var out []ClientType
	for _, c := range a {
		if set[c] {
			out = append(out, c)
		}
	}
	return out
}
