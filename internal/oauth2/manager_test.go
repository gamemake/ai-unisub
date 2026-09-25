package oauth2

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ai-unisub/internal/database"
	"ai-unisub/internal/proxy2"
)

func TestManagerPKCEFlowRecordsOutboundCall(t *testing.T) {
	db, proxyManager := openTestDependencies(t)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	manager := NewManager(db, proxyManager)
	adapter := &testPKCEAdapter{service: "test-pkce", endpoint: server.URL}
	if err := manager.Register(adapter); err != nil {
		t.Fatal(err)
	}
	started, err := manager.Start(t.Context(), adapter.service, "subject", "http://127.0.0.1/callback", 0)
	if err != nil {
		t.Fatal(err)
	}
	session, err := manager.SessionForSubject(started.SessionID, adapter.service, "subject")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Complete(t.Context(), started.SessionID, "code", "wrong"); !errors.Is(err, ErrStateMismatch) {
		t.Fatalf("expected state mismatch, got %v", err)
	}
	credential, err := manager.Complete(t.Context(), started.SessionID, "code", session.State)
	if err != nil {
		t.Fatal(err)
	}
	if credential.AccessToken != "access" {
		t.Fatalf("unexpected credential: %+v", credential)
	}
	if _, err := manager.SessionForState(session.State); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("completed session was not consumed: %v", err)
	}

	logs, total, err := db.QueryProxyLogs(database.ProxyLogFilter{
		GroupID: 0, AppType: "oauth:" + adapter.service,
		TimeRange: database.TimeRange{Start: time.Now().Add(-time.Minute), End: time.Now().Add(time.Minute)},
	}, 1, 10)
	if err != nil || total != 1 || len(logs) != 1 || logs[0].HTTPErrorCode != http.StatusCreated {
		t.Fatalf("outbound OAuth call was not recorded: logs=%+v total=%d err=%v", logs, total, err)
	}
}

func TestManagerDummyDeviceFlow(t *testing.T) {
	_, proxyManager := openTestDependencies(t)
	manager := NewManager(nil, proxyManager)
	started, err := manager.Start(t.Context(), OAuthServiceDummy, "subject", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if started.UserCode == "" || started.VerificationURI == "" {
		t.Fatalf("missing device authorization fields: %+v", started)
	}
	if _, err := manager.Poll(t.Context(), started.SessionID); !errors.Is(err, ErrAuthorizationPending) {
		t.Fatalf("expected pending result, got %v", err)
	}
	credential, err := manager.Poll(t.Context(), started.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if credential.AccessToken == "" {
		t.Fatal("device flow returned an empty access token")
	}
	if _, err := manager.Poll(t.Context(), started.SessionID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("successful device session was not consumed: %v", err)
	}
}

func TestManagerRefreshPreservesCredentialMetadata(t *testing.T) {
	_, proxyManager := openTestDependencies(t)
	manager := NewManager(nil, proxyManager)
	adapter := &testPKCEAdapter{service: "test-refresh", refresh: &OAuthCredential{AccessToken: "new"}}
	if err := manager.Register(adapter); err != nil {
		t.Fatal(err)
	}
	old := &OAuthCredential{AccessToken: "old", RefreshToken: "refresh", TokenType: "Bearer", AccountID: "account", AccountName: "name", Email: "mail@example.test"}
	refreshed, err := manager.Refresh(t.Context(), adapter.service, old, 0)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.RefreshToken != old.RefreshToken || refreshed.AccountID != old.AccountID || refreshed.AccountName != old.AccountName || refreshed.Email != old.Email {
		t.Fatalf("refresh metadata was not preserved: %+v", refreshed)
	}
}

type testPKCEAdapter struct {
	service  string
	endpoint string
	refresh  *OAuthCredential
}

func (a *testPKCEAdapter) Service() string { return a.service }

func (a *testPKCEAdapter) BuildAuthorizationURL(_ context.Context, _ AuthorizationInput) (AuthorizationResult, error) {
	return AuthorizationResult{AuthorizationURL: "https://example.test/authorize"}, nil
}

func (a *testPKCEAdapter) Exchange(ctx context.Context, _, _, _, _ string, client *http.Client) (*OAuthCredential, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	return &OAuthCredential{AccessToken: "access"}, nil
}

func (a *testPKCEAdapter) Refresh(_ context.Context, _ *OAuthCredential, _ *http.Client) (*OAuthCredential, error) {
	return a.refresh, nil
}

func openTestDependencies(t *testing.T) (database.Database, proxy2.ProxyManager) {
	t.Helper()
	db, err := database.NewDatabase("sqlite::memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Open(); err != nil {
		t.Fatal(err)
	}
	manager := proxy2.NewManager(db)
	if err := manager.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Error(err)
		}
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db, manager
}
