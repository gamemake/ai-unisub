package aiprovider

// Code-owned defaults for usage requests. Treat as read-only: callers copy
// before applying supplier overrides and externally supplied authentication.
// Vendor-specific protocol headers belong to the corresponding query code.
var defaultQuotaRequestHeaders = map[string]string{
	"Accept":       "application/json",
	"Content-Type": "application/json",
}

// SupplierConfigs returns the built-in supplier names and service endpoints.
func SupplierConfigs() []Supplier {
	return []Supplier{
		{ID: "anthropic", Name: "Anthropic", ClaudeURL: "https://api.anthropic.com/v1"},
		{ID: "openai", Name: "OpenAI", CodexURL: "https://api.openai.com/v1"},
		{ID: "grok", Name: "Grok", CodexURL: "https://api.x.ai/v1"},
		{ID: "deepseek", Name: "Deepseek", ClaudeURL: "https://api.deepseek.com/anthropic/v1", CodexURL: "https://api.deepseek.com/v1"},
		{ID: "zhipu", Name: "智谱", ClaudeURL: "https://open.bigmodel.cn/api/anthropic/v1", CodexURL: "https://open.bigmodel.cn/api/paas/v4"},
		{ID: "kimi", Name: "Kimi", ClaudeURL: "https://api.moonshot.cn/anthropic/v1", CodexURL: "https://api.moonshot.cn/v1"},
	}
}

func BuiltinSuppliers() []Supplier { return SupplierConfigs() }
