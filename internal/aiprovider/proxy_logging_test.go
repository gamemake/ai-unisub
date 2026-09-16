package aiprovider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"ai-unisub/internal/proxy"
)

type proxyAttemptResult struct {
	class  proxy.ErrorClass
	cause  error
	status int
}

type detailedProxyResolver struct {
	endpoints []*proxy.Endpoint
	selected  int
	results   []proxyAttemptResult
}

func (r *detailedProxyResolver) ResolveProxy(context.Context, string, string, []string) (*proxy.Endpoint, error) {
	e := r.endpoints[r.selected]
	r.selected++
	return e, nil
}
func (r *detailedProxyResolver) ProxyRetryLimit(string) int { return len(r.endpoints) - 1 }
func (r *detailedProxyResolver) ReportProxy(*proxy.Endpoint, string, proxy.ErrorClass) error {
	panic("detailed reporting must be preferred")
}
func (r *detailedProxyResolver) ReportProxyResult(_ *proxy.Endpoint, _ string, class proxy.ErrorClass, cause error, status int) error {
	r.results = append(r.results, proxyAttemptResult{class, cause, status})
	return nil
}

func TestProxyAttemptsIncludeErrorDetailsAndHTTPStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		class  proxy.ErrorClass
		retry  bool
	}{
		{"server_failure", 503, proxy.ApplicationError, true},
		{"ignored_application_failure", 401, proxy.ApplicationIgnored, false},
		{"network_failure", 0, proxy.NetworkError, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tc.status) }))
			defer first.Close()
			if tc.status == 0 {
				first.Close()
			}
			e, err := proxy.NewEndpoint(first.URL)
			if err != nil {
				t.Fatal(err)
			}
			resolver := &detailedProxyResolver{endpoints: []*proxy.Endpoint{e}}
			if tc.retry {
				second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
				defer second.Close()
				e, err := proxy.NewEndpoint(second.URL)
				if err != nil {
					t.Fatal(err)
				}
				resolver.endpoints = append(resolver.endpoints, e)
			}
			p := &oauthAIProvider{
				config:   AIProviderConfig{AuthType: AuthTypeAPIKey, APIKey: "not-logged", ProxyGroupID: "g", Supplier: "test"},
				client:   upstreamClient(http.DefaultTransport.(*http.Transport).Clone()),
				resolver: resolver,
			}
			req := httptest.NewRequest(http.MethodGet, "http://target.invalid/resource", nil)
			p.handle("test", "", req, nil)
			if len(resolver.results) != len(resolver.endpoints) {
				t.Fatalf("expected each attempt to report: %+v", resolver.results)
			}
			result := resolver.results[0]
			if result.class != tc.class || result.status != tc.status || (result.cause != nil) != (tc.status == 0) {
				t.Fatalf("lost failure details: %+v", result)
			}
			if tc.retry && (resolver.results[1].class != proxy.Success || resolver.results[1].status != 200) {
				t.Fatalf("unexpected retry report: %+v", resolver.results[1])
			}
		})
	}
}
