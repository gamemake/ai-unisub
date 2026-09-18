package aiprovider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"ai-unisub/internal/proxy"
)

type callbackProxyResolver struct {
	results int
}

func (r *callbackProxyResolver) ResolveProxy(context.Context, int, string, []string) (*proxy.Endpoint, error) {
	r.results++
	return proxy.NewEndpoint("http://127.0.0.1:9")
}
func (r *callbackProxyResolver) ProxyRetryLimit(int) int { return 0 }
func (r *callbackProxyResolver) ReportProxy(*proxy.Endpoint, string, proxy.ErrorClass) error {
	return nil
}

func TestProxyCallbackReceivesRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	resolver := &callbackProxyResolver{}
	p := &oauthAIProvider{config: AIProviderConfig{AuthType: AuthTypeAPIKey, APIKey: "key", ProxyGroupID: 1, Supplier: "test"}, resolver: resolver}
	p.handle("test", "", httptest.NewRequest(http.MethodGet, server.URL, nil), nil)
	if resolver.results != 1 {
		t.Fatalf("expected one proxy callback, got %d", resolver.results)
	}
}
