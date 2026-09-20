package aiprovider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"ai-unisub/internal/proxy"
)

func TestParseModelsResponse(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want []string
		err  error
	}{
		{
			name: "openai list",
			body: `{"object":"list","data":[{"id":"gpt-4.1"},{"id":"gpt-5"},{"id":"gpt-4.1"}]}`,
			want: []string{"gpt-4.1", "gpt-5"},
		},
		{
			name: "anthropic list",
			body: `{"data":[{"id":"claude-sonnet-4-6","type":"model"},{"id":"claude-opus-4-6","type":"model"}],"has_more":false}`,
			want: []string{"claude-opus-4-6", "claude-sonnet-4-6"},
		},
		{
			name: "models field",
			body: `{"models":[" b ","a",""]}`,
			want: []string{"a", "b"},
		},
		{
			name: "string array",
			body: `["z","a"]`,
			want: []string{"a", "z"},
		},
		{
			name: "empty data",
			body: `{"data":[]}`,
			err:  ErrModelsInvalidResponse,
		},
		{
			name: "invalid json",
			body: `{`,
			err:  ErrModelsInvalidResponse,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseModelsResponse([]byte(tc.body))
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("err=%v want %v", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestFetchModelsAPIProvider(t *testing.T) {
	t.Parallel()
	var sawAuth string
	p := &oauthAIProvider{
		config: AIProviderConfig{
			ID: 1, Kind: "api", Supplier: "deepseek", AuthType: AuthTypeAPIKey,
			APIKey: "secret", APIEndpoint: "https://api.deepseek.com/v1",
		},
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodGet || r.URL.String() != "https://api.deepseek.com/v1/models" {
				t.Fatalf("unexpected request %s %s", r.Method, r.URL)
			}
			sawAuth = r.Header.Get("Authorization")
			body, _ := json.Marshal(map[string]any{
				"object": "list",
				"data":   []map[string]string{{"id": "m-b"}, {"id": "m-a"}},
			})
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(string(body))),
				Header:     make(http.Header),
				Request:    r,
			}, nil
		})},
	}
	models, err := p.FetchModels(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if sawAuth != "Bearer secret" {
		t.Fatalf("auth=%q", sawAuth)
	}
	if strings.Join(models, ",") != "m-a,m-b" {
		t.Fatalf("models=%v", models)
	}
}

func TestFetchModelsOpenAIAPIProviderFallsBackToSubscriptionCatalog(t *testing.T) {
	t.Parallel()
	var requests []string
	p := &oauthAIProvider{
		config: AIProviderConfig{
			ID: 1, Kind: "api", Supplier: "openai", AuthType: AuthTypeAPIKey,
			APIKey: "secret", APIEndpoint: "https://api.openai.com/v1",
		},
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			requests = append(requests, r.URL.String())
			if r.URL.String() == "https://api.openai.com/v1/models" {
				return &http.Response{
					StatusCode: http.StatusBadGateway,
					Body:       io.NopCloser(strings.NewReader("gateway unavailable")),
					Header:     make(http.Header),
					Request:    r,
				}, nil
			}
			if r.URL.String() != codexModelsJSONURL {
				t.Fatalf("unexpected request %s", r.URL)
			}
			if got := r.Header.Get("Authorization"); got != "" {
				t.Fatalf("fallback must not send API key: %q", got)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"models":[{"slug":"gpt-fallback"}]}`)),
				Header:     make(http.Header),
				Request:    r,
			}, nil
		})},
	}
	models, err := p.FetchModels(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(models, ",") != "gpt-fallback" {
		t.Fatalf("models=%v", models)
	}
	if strings.Join(requests, ",") != "https://api.openai.com/v1/models,"+codexModelsJSONURL {
		t.Fatalf("requests=%v", requests)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchModelsDummyAndGroup(t *testing.T) {
	t.Parallel()
	dummy, err := NewDummyAIProvider(1, ProviderData{Config: json.RawMessage(`{"name":"d","enabled":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	models, err := dummy.FetchModels(t.Context())
	if err != nil || len(models) == 0 {
		t.Fatalf("dummy models=%v err=%v", models, err)
	}
	if _, err := dummy.FetchModels(canceledContext(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled err=%v", err)
	}
	group, err := newGroup(2, ProviderData{Config: json.RawMessage(`{"kind":"group","members":[{"id":1,"weight":3}],"enabled":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := group.FetchModels(t.Context()); !errors.Is(err, ErrModelsUnsupported) {
		t.Fatalf("group err=%v", err)
	}
}

func TestModelsEndpointOpenAISubscriptionUnsupported(t *testing.T) {
	t.Parallel()
	_, service, err := modelsEndpoint(AIProviderConfig{Supplier: "openai", AuthType: AuthTypeOAuth}, nil)
	if service != "" || !errors.Is(err, ErrModelsUnsupported) {
		t.Fatalf("service=%q err=%v", service, err)
	}
}

func TestModelsEndpointAPIUsesConfiguredURL(t *testing.T) {
	t.Parallel()
	endpoint, service, err := modelsEndpoint(AIProviderConfig{
		Supplier: "deepseek", AuthType: AuthTypeAPIKey, APIEndpoint: "https://api.deepseek.com/v1",
	}, nil)
	if err != nil || service != "" || endpoint != "https://api.deepseek.com/v1/models" {
		t.Fatalf("endpoint=%q service=%q err=%v", endpoint, service, err)
	}
}

func TestParseCodexModelsJSON(t *testing.T) {
	t.Parallel()
	got, err := parseCodexModelsJSON([]byte(`{"models":[{"slug":"gpt-5.4"},{"slug":" gpt-5.5 "},{"slug":"gpt-5.4"},{"slug":""}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "gpt-5.4,gpt-5.5" {
		t.Fatalf("got %v", got)
	}
	if _, err := parseCodexModelsJSON([]byte(`{"models":[]}`)); !errors.Is(err, ErrModelsInvalidResponse) {
		t.Fatalf("empty err=%v", err)
	}
	if _, err := parseCodexModelsJSON([]byte(`{`)); !errors.Is(err, ErrModelsInvalidResponse) {
		t.Fatalf("invalid err=%v", err)
	}
}

func TestFetchModelsOpenAISubscriptionUsesCodexCatalog(t *testing.T) {
	t.Parallel()
	var sawURL string
	var sawAuth string
	p := &oauthAIProvider{
		config: AIProviderConfig{
			ID: 1, Kind: "subscription", Supplier: "openai", AuthType: AuthTypeOAuth, CredentialID: "cred",
		},
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			sawURL = r.URL.String()
			sawAuth = r.Header.Get("Authorization")
			if r.Method != http.MethodGet || r.URL.String() != codexModelsJSONURL {
				t.Fatalf("unexpected request %s %s", r.Method, r.URL)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"models":[{"slug":"gpt-b"},{"slug":"gpt-a"}]}`)),
				Header:     make(http.Header),
				Request:    r,
			}, nil
		})},
	}
	models, err := p.FetchModels(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if sawURL != codexModelsJSONURL {
		t.Fatalf("url=%q", sawURL)
	}
	if sawAuth != "" {
		t.Fatalf("public catalog must not send auth: %q", sawAuth)
	}
	if strings.Join(models, ",") != "gpt-a,gpt-b" {
		t.Fatalf("models=%v", models)
	}
}

func TestFetchModelsOpenAISubscriptionUsesProxyGroup(t *testing.T) {
	t.Parallel()
	// HTTP origin + forward proxy. modelsHTTPClient uses proxy.Client (ProxyURL).
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex-rs/models-manager/models.json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"models":[{"slug":"gpt-proxy"}]}`))
	}))
	defer origin.Close()
	originURL, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatal(err)
	}

	var proxied int
	forward := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied++
		if r.URL.Host != originURL.Host {
			t.Fatalf("proxy host=%q want %q", r.URL.Host, originURL.Host)
		}
		resp, err := http.DefaultTransport.RoundTrip(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	defer forward.Close()

	resolver := &countingProxyResolver{proxyURL: forward.URL}
	p := &oauthAIProvider{
		config: AIProviderConfig{
			ID: 1, Kind: "subscription", Supplier: "openai", AuthType: AuthTypeOAuth,
			CredentialID: "cred", ProxyGroupID: 7,
		},
		resolver: resolver,
	}
	client, err := p.modelsHTTPClient(t.Context(), p.config, nil, p.resolver, true)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, origin.URL+"/codex-rs/models-manager/models.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, status, err := doModelsGET(t.Context(), client, req)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	models, err := parseCodexModelsJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	if resolver.hits != 1 {
		t.Fatalf("proxy hits=%d want 1", resolver.hits)
	}
	if proxied < 1 {
		t.Fatal("request did not go through forward proxy")
	}
	if strings.Join(models, ",") != "gpt-proxy" {
		t.Fatalf("models=%v", models)
	}
}

func TestFetchModelsOpenAISubscriptionRequiresProxyWhenConfigured(t *testing.T) {
	t.Parallel()
	p := &oauthAIProvider{
		config: AIProviderConfig{
			Supplier: "openai", AuthType: AuthTypeOAuth, ProxyGroupID: 3,
		},
	}
	if _, err := p.FetchModels(t.Context()); !errors.Is(err, ErrModelsNotConfigured) {
		t.Fatalf("err=%v", err)
	}
	resolver := &countingProxyResolver{err: errors.New("no healthy proxy")}
	p.resolver = resolver
	if _, err := p.FetchModels(t.Context()); !errors.Is(err, ErrModelsUpstream) {
		t.Fatalf("err=%v", err)
	}
	if resolver.hits != 1 {
		t.Fatalf("hits=%d", resolver.hits)
	}
}

type countingProxyResolver struct {
	hits     int
	proxyURL string
	err      error
}

func (r *countingProxyResolver) ResolveProxy(_ context.Context, groupID int, app string, _ []string) (*proxy.Endpoint, error) {
	r.hits++
	if r.err != nil {
		return nil, r.err
	}
	if groupID == 0 {
		return nil, errors.New("missing group")
	}
	if app != "openai" {
		return nil, errors.New("unexpected app " + app)
	}
	return proxy.NewEndpoint(r.proxyURL)
}
func (r *countingProxyResolver) ProxyRetryLimit(int) int { return 0 }
func (r *countingProxyResolver) ReportProxy(*proxy.Endpoint, string, proxy.ErrorClass) error {
	return nil
}

func canceledContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	return ctx
}
