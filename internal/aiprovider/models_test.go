package aiprovider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
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

func canceledContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	return ctx
}
