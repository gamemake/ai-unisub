package oauth

import (
	"ai-unisub/internal/proxy"
	"context"
	"sync"
	"testing"
)

type endpointAdapter struct {
	mu   sync.Mutex
	seen []*proxy.Endpoint
}

func (a *endpointAdapter) Service() string { return "explicit" }
func (a *endpointAdapter) BuildAuthorizationURL(ctx context.Context, in AuthorizationInput) (AuthorizationResult, error) {
	return AuthorizationResult{AuthorizationURL: in.State}, nil
}
func (a *endpointAdapter) Exchange(ctx context.Context, code, state, verifier, redirect string, endpoints ...*proxy.Endpoint) (*OAuthCredential, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seen = append(a.seen, endpoints[0])
	return &OAuthCredential{AccessToken: "token"}, nil
}
func (a *endpointAdapter) Refresh(ctx context.Context, c *OAuthCredential, endpoints ...*proxy.Endpoint) (*OAuthCredential, error) {
	return a.Exchange(ctx, "", "", "", "", endpoints...)
}
func TestConcurrentSessionEndpointsRemainBound(t *testing.T) {
	m := NewManager(nil)
	a := &endpointAdapter{}
	if err := m.Register(a); err != nil {
		t.Fatal(err)
	}
	first, _ := proxy.NewEndpoint("http://localhost:9001")
	second, _ := proxy.NewEndpoint("socks5h://localhost:9002")
	var wg sync.WaitGroup
	for _, endpoint := range []*proxy.Endpoint{first, second, nil} {
		wg.Go(func() {
			start, err := m.Start(t.Context(), a.Service(), "subject", "http://callback", endpoint)
			if err != nil {
				t.Error(err)
				return
			}
			session, err := m.session(start.SessionID, false)
			if err != nil {
				t.Error(err)
				return
			}
			if _, err = m.Complete(t.Context(), start.SessionID, "code", session.State); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	seen := map[*proxy.Endpoint]bool{}
	for _, e := range a.seen {
		seen[e] = true
	}
	if len(seen) != 3 || !seen[first] || !seen[second] || !seen[nil] {
		t.Fatal("session endpoints crossed")
	}
}
