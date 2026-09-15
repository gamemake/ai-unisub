package aiprovider

import (
	"errors"
	"strings"
)

type Supplier struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ClaudeURL string `json:"claude_url"`
	CodexURL  string `json:"codex_url"`
}
type Catalog struct {
	Suppliers []Supplier `json:"suppliers"`
}

// ModuleConfigKey identifies this module's persisted configuration document.
const ModuleConfigKey = "aiprovider"

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
func cloneCatalog(c Catalog) Catalog {
	c.Suppliers = append([]Supplier{}, c.Suppliers...)
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
