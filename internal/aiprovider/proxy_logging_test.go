package aiprovider

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type callbackProxyResolver struct {
	clients []*http.Client
	results int
}

func (r *callbackProxyResolver) Do(_ int, _ string, callback func(*http.Client) (string, int, string)) {
	if len(r.clients) == 0 {
		return
	}
	c := r.clients[0]
	r.clients = r.clients[1:]
	callback(c)
	r.results++
}

func TestProxyCallbackReceivesRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	resolver := &callbackProxyResolver{clients: []*http.Client{{Transport: http.DefaultTransport}}}
	p := &oauthAIProvider{config: AIProviderConfig{AuthType: AuthTypeAPIKey, APIKey: "key", ProxyGroupID: 1, Supplier: "test"}, resolver: resolver}
	p.handle("test", "", httptest.NewRequest(http.MethodGet, server.URL, nil), nil)
	if resolver.results != 1 {
		t.Fatalf("expected one proxy callback, got %d", resolver.results)
	}
}
