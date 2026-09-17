package oauth

import (
	"context"
	"net/http"
	"sync"
	"testing"
)

type endpointAdapter struct {
	mu   sync.Mutex
	seen []*http.Client
}

func (a *endpointAdapter) Service() string { return "explicit" }
func (a *endpointAdapter) BuildAuthorizationURL(ctx context.Context, in AuthorizationInput) (AuthorizationResult, error) {
	return AuthorizationResult{AuthorizationURL: in.State}, nil
}
func (a *endpointAdapter) Exchange(ctx context.Context, code, state, verifier, redirect string, client *http.Client) (*OAuthCredential, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seen = append(a.seen, client)
	return &OAuthCredential{AccessToken: "token"}, nil
}
func (a *endpointAdapter) Refresh(ctx context.Context, c *OAuthCredential, client *http.Client) (*OAuthCredential, error) {
	return a.Exchange(ctx, "", "", "", "", client)
}
func TestConcurrentSessionClientsRemainBound(t *testing.T) {
	m := NewManager(nil)
	a := &endpointAdapter{}
	if err := m.Register(a); err != nil {
		t.Fatal(err)
	}
	first, second := &http.Client{}, &http.Client{}
	var wg sync.WaitGroup
	for _, client := range []*http.Client{first, second, nil} {
		wg.Go(func() {
			start, err := m.Start(t.Context(), a.Service(), "subject", "http://callback", client)
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
	seen := map[*http.Client]bool{}
	for _, e := range a.seen {
		seen[e] = true
	}
	if len(seen) != 3 || !seen[first] || !seen[second] || !seen[nil] {
		t.Fatal("session clients crossed")
	}
}
