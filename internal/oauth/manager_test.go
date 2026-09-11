package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

type testAdapter struct {
	mu      sync.Mutex
	refresh int
	service string
	expires time.Duration
}

func (a *testAdapter) Service() string { return a.service }
func (a *testAdapter) BuildAuthorizationURL(_ context.Context, in AuthorizationInput) (AuthorizationResult, error) {
	return AuthorizationResult{AuthorizationURL: "https://example.test/authorize?state=" + in.State}, nil
}
func (a *testAdapter) Exchange(_ context.Context, _, _, _, _ string) (*OAuthCredential, error) {
	return &OAuthCredential{AccessToken: "access", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (a *testAdapter) Refresh(_ context.Context, old *OAuthCredential) (*OAuthCredential, error) {
	a.mu.Lock()
	a.refresh++
	a.mu.Unlock()
	return &OAuthCredential{AccessToken: "refreshed", RefreshToken: old.RefreshToken, ExpiresAt: time.Now().Add(a.expires)}, nil
}

type testStore struct {
	mu    sync.Mutex
	value json.RawMessage
}

func (s *testStore) LoadCredential(string) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.value) == 0 {
		return nil, errors.New("missing")
	}
	return append(json.RawMessage(nil), s.value...), nil
}
func (s *testStore) SaveCredential(_ string, v json.RawMessage) error {
	s.mu.Lock()
	s.value = append(json.RawMessage(nil), v...)
	s.mu.Unlock()
	return nil
}
func (s *testStore) DeleteCredential(string) error { return nil }

func TestManagerPKCEStateAndOneTimeSession(t *testing.T) {
	m := NewManager(nil)
	adapter := &testAdapter{service: OAuthServiceClaude}
	if err := m.Register(adapter); err != nil {
		t.Fatal(err)
	}
	start, err := m.Start(context.Background(), OAuthServiceClaude, "subject", "http://127.0.0.1/callback")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Complete(context.Background(), start.SessionID, "code", "wrong"); !errors.Is(err, ErrStateMismatch) {
		t.Fatalf("state error=%v", err)
	}
	s, err := m.session(start.SessionID, false)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := m.Complete(context.Background(), start.SessionID, "code", s.State)
	if err != nil {
		t.Fatal(err)
	}
	if credential.AccessToken != "access" {
		t.Fatalf("credential=%+v", credential)
	}
	if _, err := m.Complete(context.Background(), start.SessionID, "code", s.State); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("replay error=%v", err)
	}
}

func TestManagerSessionLookupBindsStateAndSubject(t *testing.T) {
	m := NewManager(nil)
	if err := m.Register(&testAdapter{service: OAuthServiceClaude}); err != nil {
		t.Fatal(err)
	}
	start, err := m.Start(context.Background(), OAuthServiceClaude, "user-1", "http://127.0.0.1/callback")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := m.session(start.SessionID, false)
	if err != nil {
		t.Fatal(err)
	}
	session, err := m.SessionForState(stored.State)
	if err != nil || session.ID != start.SessionID || session.SubjectID != "user-1" {
		t.Fatalf("session=%+v err=%v", session, err)
	}
	if _, err := m.SessionForSubject(start.SessionID, OAuthServiceClaude, "user-2"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("cross-subject lookup error=%v", err)
	}
}

func TestManagerRefreshesCredentialOnceConcurrently(t *testing.T) {
	store := &testStore{}
	encoded, _ := json.Marshal(&OAuthCredential{AccessToken: "old", RefreshToken: "refresh", ExpiresAt: time.Now().Add(-time.Minute)})
	_ = store.SaveCredential("id", encoded)
	adapter := &testAdapter{service: OAuthServiceClaude, expires: time.Hour}
	m := NewManager(store)
	_ = m.Register(adapter)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := m.GetValidAccessToken(context.Background(), OAuthServiceClaude, "id")
			if err != nil || token != "refreshed" {
				t.Errorf("token=%q err=%v", token, err)
			}
		}()
	}
	wg.Wait()
	adapter.mu.Lock()
	calls := adapter.refresh
	adapter.mu.Unlock()
	if calls != 1 {
		t.Fatalf("refresh calls=%d", calls)
	}
}
