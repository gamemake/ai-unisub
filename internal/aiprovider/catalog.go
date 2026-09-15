package aiprovider

import (
	"encoding/json"
	"errors"
	"strings"
)

type Supplier struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	ClaudeURL string         `json:"claude_url"`
	CodexURL  string         `json:"codex_url"`
	Mappings  []ModelMapping `json:"mappings"`
}
type ModelMapping struct {
	Client ClientType `json:"client"`
	Model  string     `json:"model"`
	Target string     `json:"target"`
}
type Catalog struct {
	Suppliers []Supplier `json:"suppliers"`
}

// ModuleConfigKey identifies this module's persisted configuration document.
const ModuleConfigKey = "aiprovider"

// UnmarshalJSON retains previously saved global overrides inside their supplier.
// New writes always use supplier-owned mappings, never a global mapping list.
func (c *Catalog) UnmarshalJSON(raw []byte) error {
	type plain Catalog
	var wire struct {
		plain
		Mappings []struct {
			ModelMapping
			Supplier string `json:"supplier"`
		} `json:"mappings"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return err
	}
	for _, mapping := range wire.Mappings {
		found := false
		for i := range wire.Suppliers {
			if wire.Suppliers[i].ID == mapping.Supplier {
				wire.Suppliers[i].Mappings = append(wire.Suppliers[i].Mappings, mapping.ModelMapping)
				found = true
				break
			}
		}
		if !found {
			return errors.New("unknown supplier in saved model mapping")
		}
	}
	*c = Catalog(wire.plain)
	return nil
}

func ValidateCatalog(c Catalog) error {
	known := map[string]Supplier{}
	for _, s := range BuiltinSuppliers() {
		known[s.ID] = s
	}
	seen := map[string]bool{}
	for _, s := range c.Suppliers {
		if known[s.ID].ID == "" || seen[s.ID] || strings.TrimSpace(s.Name) == "" {
			return errors.New("invalid or duplicate supplier")
		}
		if s.ClaudeURL != known[s.ID].ClaudeURL || s.CodexURL != known[s.ID].CodexURL {
			return errors.New("built-in supplier URLs cannot be modified")
		}
		seen[s.ID] = true
		keys := map[string]bool{}
		for _, v := range s.Mappings {
			if (v.Client != ClientAnthropic && v.Client != ClientOpenAI && v.Client != ClientGrok) || strings.TrimSpace(v.Model) == "" || strings.TrimSpace(v.Target) == "" || v.Model != strings.TrimSpace(v.Model) || v.Target != strings.TrimSpace(v.Target) {
				return errors.New("invalid model mapping")
			}
			key := mappingKey(v)
			if keys[key] {
				return errors.New("duplicate model mapping within supplier")
			}
			keys[key] = true
		}
	}
	if len(seen) != len(known) {
		return errors.New("built-in suppliers cannot be deleted")
	}
	return nil
}
func mappingKey(v ModelMapping) string { return string(v.Client) + "\x00" + v.Model }
func (m *AIProviderManager) Catalog() Catalog {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneCatalog(m.catalog)
}
func cloneCatalog(c Catalog) Catalog {
	c.Suppliers = append([]Supplier{}, c.Suppliers...)
	for i := range c.Suppliers {
		c.Suppliers[i].Mappings = append([]ModelMapping{}, c.Suppliers[i].Mappings...)
	}
	return c
}
func (m *AIProviderManager) SetCatalog(c Catalog) error {
	if err := ValidateCatalog(c); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.catalog = cloneCatalog(c)
	return nil
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
func (m *AIProviderManager) MapModel(client ClientType, supplier, model string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.catalog.Suppliers {
		if s.ID != supplier {
			continue
		}
		for _, v := range s.Mappings {
			if v.Client == client && v.Model == model {
				return v.Target
			}
		}
		for _, v := range BuiltinMappings(supplier) {
			if v.Client == client && v.Model == model {
				return v.Target
			}
		}
		break
	}
	return model
}

// URLForClient uses the shared Codex endpoint for Grok.
func (s Supplier) URLForClient(client ClientType) string {
	if client == ClientAnthropic {
		return s.ClaudeURL
	}
	return s.CodexURL
}

// RestoreBuiltinURLs is only used when loading stored configuration.
// Persisted URL overrides never replace code-owned built-in endpoints.
func RestoreBuiltinURLs(c *Catalog) {
	for i := range c.Suppliers {
		for _, builtin := range SupplierConfigs() {
			if c.Suppliers[i].ID == builtin.ID {
				c.Suppliers[i].ClaudeURL = builtin.ClaudeURL
				c.Suppliers[i].CodexURL = builtin.CodexURL
				break
			}
		}
	}
}

// EndpointClient follows the wire protocol, including requests from generic SDKs.
func EndpointClient(path string) ClientType {
	if strings.HasSuffix(path, "/messages") || strings.HasSuffix(path, "/messages/count_tokens") {
		return ClientAnthropic
	}
	return ClientOpenAI
}
