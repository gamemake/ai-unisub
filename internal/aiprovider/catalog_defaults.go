package aiprovider

// Code-owned defaults for usage requests. Treat as read-only: callers copy
// before applying supplier overrides and externally supplied authentication.
// Vendor-specific protocol headers belong to the corresponding query code.
var defaultQuotaRequestHeaders = map[string]string{
	"Accept":       "application/json",
	"Content-Type": "application/json",
}

// SupplierBuiltin is the code-owned supplier definition. URLs and default
// models are not configurable at runtime.
type SupplierBuiltin struct {
	ID        string
	Name      string
	ClaudeURL string
	OpenAIURL string
	Models    []string
}

// SupplierBuiltins returns the fixed supplier table.
func SupplierBuiltins() []SupplierBuiltin {
	return []SupplierBuiltin{
		{
			ID: "anthropic", Name: "Anthropic",
			ClaudeURL: "https://api.anthropic.com/v1",
			Models:    []string{"claude-opus-4-6", "claude-sonnet-4-6", "claude-haiku-4-5-20251001"},
		},
		{
			ID: "openai", Name: "OpenAI",
			OpenAIURL: "https://api.openai.com/v1",
			Models:    []string{"gpt-5", "gpt-5-mini", "gpt-4.1", "o3", "o4-mini"},
		},
		{
			ID: "grok", Name: "xAI",
			OpenAIURL: "https://api.x.ai/v1",
			Models:    []string{"grok-4", "grok-3", "grok-3-mini"},
		},
		{
			ID: "deepseek", Name: "Deepseek",
			ClaudeURL: "https://api.deepseek.com/anthropic/v1",
			OpenAIURL: "https://api.deepseek.com/v1",
			Models:    []string{"deepseek-chat", "deepseek-reasoner"},
		},
		{
			ID: "zhipu", Name: "智谱",
			ClaudeURL: "https://open.bigmodel.cn/api/anthropic/v1",
			OpenAIURL: "https://open.bigmodel.cn/api/paas/v4",
			Models:    []string{"glm-4.5", "glm-4.5-air", "glm-4.5-flash"},
		},
		{
			ID: "kimi", Name: "Kimi",
			ClaudeURL: "https://api.moonshot.cn/anthropic/v1",
			OpenAIURL: "https://api.moonshot.cn/v1",
			Models:    []string{"kimi-k2-turbo-preview", "moonshot-v1-auto"},
		},
	}
}

// SupplierConfigs returns merged suppliers with no overlays (code defaults).
func SupplierConfigs() []Supplier {
	out := make([]Supplier, 0, len(SupplierBuiltins()))
	for _, b := range SupplierBuiltins() {
		out = append(out, MergeSupplier(b, SupplierOverlay{}))
	}
	return out
}

// BuiltinSuppliers returns code defaults as the public Supplier view.
func BuiltinSuppliers() []Supplier { return SupplierConfigs() }

func builtinByID(id string) (SupplierBuiltin, bool) {
	for _, b := range SupplierBuiltins() {
		if b.ID == id {
			return b, true
		}
	}
	return SupplierBuiltin{}, false
}
