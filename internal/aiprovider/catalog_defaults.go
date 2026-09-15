package aiprovider

// SupplierConfigs returns writable overrides. Built-in mappings are kept in code
// so deleting an override restores the default without copying it into storage.
func SupplierConfigs() []Supplier {
	return []Supplier{
		{ID: "anthropic", Name: "Anthropic", ClaudeURL: "https://api.anthropic.com/v1", Mappings: []ModelMapping{}},
		{ID: "openai", Name: "OpenAI", CodexURL: "https://api.openai.com/v1", Mappings: []ModelMapping{}},
		{ID: "grok", Name: "Grok", CodexURL: "https://api.x.ai/v1", Mappings: []ModelMapping{}},
		{ID: "deepseek", Name: "Deepseek", ClaudeURL: "https://api.deepseek.com/anthropic/v1", CodexURL: "https://api.deepseek.com/v1", Mappings: []ModelMapping{}},
		{ID: "zhipu", Name: "智谱", ClaudeURL: "https://open.bigmodel.cn/api/anthropic/v1", CodexURL: "https://open.bigmodel.cn/api/paas/v4", Mappings: []ModelMapping{}},
		{ID: "kimi", Name: "Kimi", ClaudeURL: "https://api.moonshot.cn/anthropic/v1", CodexURL: "https://api.moonshot.cn/v1", Mappings: []ModelMapping{}},
	}
}

func BuiltinSuppliers() []Supplier {
	suppliers := SupplierConfigs()
	for i := range suppliers {
		suppliers[i].Mappings = BuiltinMappings(suppliers[i].ID)
	}
	return suppliers
}

// These are UniSub routing presets, not vendor claims of equivalent capabilities.
// Sources and update boundaries are documented in docs/ai-provider.md.
// Use exact names: unknown models must not be silently caught by a wildcard.
func BuiltinMappings(supplier string) []ModelMapping {
	targets, ok := map[string][3]string{
		"anthropic": {"claude-haiku-4-5-20251001", "claude-sonnet-5", "claude-opus-5"},
		"openai":    {"gpt-5.6-luna", "gpt-5.6-terra", "gpt-5.6-sol"},
		"grok":      {"grok-build-0.1", "grok-build-0.1", "grok-build-0.1"},
		"deepseek":  {"deepseek-v4-flash", "deepseek-v4-flash", "deepseek-v4-pro"},
		"zhipu":     {"glm-4.5-air", "glm-5", "glm-5"},
		"kimi":      {"kimi-k2.5", "kimi-k2.5", "kimi-k2.5"},
	}[supplier]
	if !ok {
		return nil
	}
	type names struct {
		client ClientType
		tier   int
		models []string
	}
	groups := []names{
		{ClientAnthropic, 0, []string{"haiku", "claude-haiku-4-5", "claude-haiku-4-5-20251001", "claude-3-5-haiku-20241022"}},
		{ClientAnthropic, 1, []string{"sonnet", "claude-sonnet-5", "claude-sonnet-4-6", "claude-sonnet-4-5", "claude-sonnet-4-5-20250929", "claude-sonnet-4-20250514"}},
		{ClientAnthropic, 2, []string{"opus", "fable", "claude-fable-5", "claude-opus-5", "claude-opus-4-8", "claude-opus-4-7", "claude-opus-4-6", "claude-opus-4-5", "claude-opus-4-5-20251101"}},
		{ClientOpenAI, 0, []string{"gpt-5.6-luna", "gpt-5.4-mini", "gpt-5.1-codex-mini", "gpt-5.3-codex-spark", "codex-mini-latest"}},
		{ClientOpenAI, 1, []string{"gpt-5.6-terra", "gpt-5.4", "gpt-5.3-codex", "gpt-5.2-codex", "gpt-5.2", "gpt-5.1-codex", "gpt-5-codex", "gpt-4o"}},
		{ClientOpenAI, 2, []string{"gpt-6-astra", "gpt-5.6", "gpt-5.6-sol", "gpt-5.5", "gpt-5.1-codex-max"}},
		{ClientGrok, 1, []string{"grok-build", "grok-build-0.1", "grok-code-fast-1", "grok-4.3", "grok-4", "grok-3"}},
	}
	var result []ModelMapping
	for _, group := range groups {
		if supplier == "anthropic" && group.client == ClientAnthropic || supplier == "openai" && group.client == ClientOpenAI || supplier == "grok" && group.client == ClientGrok {
			continue
		}
		for _, model := range group.models {
			result = append(result, ModelMapping{Client: group.client, Model: model, Target: targets[group.tier]})
		}
	}
	return result
}
