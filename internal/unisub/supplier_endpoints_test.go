package unisub

import (
	"ai-unisub/internal/aiprovider"
	"net/url"
	"testing"
)

func TestSupplierEndpointRouting(t *testing.T) {
	s := testApp(t)
	for _, tc := range []struct{ supplier, path, want string }{
		{"kimi", "/v1/messages", "https://api.moonshot.cn/anthropic/v1/messages"},
		{"kimi", "/v1/messages/count_tokens", "https://api.moonshot.cn/anthropic/v1/messages/count_tokens"},
		{"kimi", "/v1/responses", "https://api.moonshot.cn/v1/responses"},
		{"kimi", "/v1/chat/completions", "https://api.moonshot.cn/v1/chat/completions"},
		{"deepseek", "/v1/messages", "https://api.deepseek.com/anthropic/v1/messages"},
		{"zhipu", "/v1/messages", "https://open.bigmodel.cn/api/anthropic/v1/messages"},
		{"zhipu", "/v1/chat/completions", "https://open.bigmodel.cn/api/paas/v4/chat/completions"},
	} {
		incoming, _ := url.Parse(tc.path)
		endpoint := s.AIProviders().DefaultURL(tc.supplier, aiprovider.EndpointClient(tc.path))
		target, err := upstreamURL(endpoint, "api", aiprovider.AuthTypeAPIKey, incoming)
		if err != nil || target.String() != tc.want {
			t.Fatal(tc, target, err)
		}
	}
	for _, supplier := range aiprovider.SupplierConfigs() {
		if s.AIProviders().DefaultURL(supplier.ID, aiprovider.ClientGrok) != s.AIProviders().DefaultURL(supplier.ID, aiprovider.ClientCodex) {
			t.Fatal("Grok must share Codex URL")
		}
	}
}
