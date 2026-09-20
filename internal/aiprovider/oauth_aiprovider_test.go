package aiprovider

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOAuthProviderTreatsCompletedResponseAsSuccess(t *testing.T) {
	const stream = "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"error\":null}}\n\n"
	p := &oauthAIProvider{
		config: AIProviderConfig{AuthType: AuthTypeAPIKey, APIKey: "test-key"},
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(stream)),
				Request:    r,
			}, nil
		})},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "https://upstream.invalid/v1/responses", strings.NewReader(`{"model":"test"}`))
	req = req.WithContext(WithResponseWriter(t.Context(), w))
	var got *AIProviderCallTrace
	p.handle("codex", "", req, func(trace *AIProviderCallTrace) { got = trace })
	if got == nil {
		t.Fatal("missing call trace")
	}
	if got.ResponseStatus != http.StatusOK || got.HTTPErrorInfo != "" {
		t.Fatalf("completed stream trace=%+v", got)
	}
	if w.Code != http.StatusOK || w.Body.String() != stream {
		t.Fatalf("response code=%d body=%q", w.Code, w.Body.String())
	}
}
