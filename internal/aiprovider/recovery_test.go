package aiprovider

import (
	"ai-unisub/internal/database"
	"ai-unisub/internal/oauth"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type recoveryAdapter struct{ calls atomic.Int32 }

func (a *recoveryAdapter) Service() string { return "claude" }
func (a *recoveryAdapter) Refresh(_ context.Context, c *oauth.OAuthCredential, _ *http.Client) (*oauth.OAuthCredential, error) {
	a.calls.Add(1)
	next := *c
	next.AccessToken = "fresh"
	return &next, nil
}
func TestOAuthRejectRefreshOnceAndAPIKeyNeverRefreshes(t *testing.T) {
	for _, alwaysReject := range []bool{false, true} {
		db := database.NewMemoryDatabase()
		_ = db.SaveCredential("cred", json.RawMessage(`{"access_token":"stale","refresh_token":"refresh"}`))
		manager := oauth.NewManager(db)
		adapter := &recoveryAdapter{}
		_ = manager.Register(adapter)
		var calls atomic.Int32
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			if alwaysReject || r.Header.Get("Authorization") != "Bearer fresh" {
				w.WriteHeader(401)
				return
			}
			w.WriteHeader(200)
		}))
		p, e := NewClaudeAIProvider("p", json.RawMessage(`{"credential_id":"cred"}`), manager)
		if e != nil {
			t.Fatal(e)
		}
		r := httptest.NewRequest("POST", upstream.URL, strings.NewReader(`{}`))
		var trace *AIProviderCallTrace
		p.Handle(r, func(v *AIProviderCallTrace) { trace = v })
		upstream.Close()
		want := 200
		if alwaysReject {
			want = 401
		}
		if trace == nil || trace.ResponseStatus != want || calls.Load() != 2 || adapter.calls.Load() != 1 {
			t.Fatal("unexpected auth recovery", trace, calls.Load(), adapter.calls.Load())
		}
	}
}
func TestOldSelectionCannotQuarantineUpdatedAccount(t *testing.T) {
	m := routingManager(t)
	createRouting(t, m, "a", "dummy", `{}`)
	s, _ := m.Select("a", "u", http.Header{}, "/v1/responses", nil)
	if err := m.UpdateConfig("a", json.RawMessage(`{"name":"updated"}`)); err != nil {
		t.Fatal(err)
	}
	m.ReportSelection(s, &AIProviderCallTrace{ResponseStatus: 401}, false)
	if _, err := m.Select("a", "u", http.Header{}, "/v1/responses", nil); err != nil {
		t.Fatal("old credential result paused replacement", err)
	}
}
func TestGroupHalfOpenQuotaIsSharedAndCancellationReleases(t *testing.T) {
	m := routingManager(t)
	createRouting(t, m, "a", "dummy", `{}`)
	createRouting(t, m, "g", "group", `{"members":[{"id":"a"}]}`)
	s, _ := m.Select("g", "u", http.Header{}, "/v1/responses", nil)
	m.ReportSelection(s, &AIProviderCallTrace{ResponseStatus: 429}, false)
	m.mu.Lock()
	m.health["a"].until = time.Now().Add(-time.Second)
	m.mu.Unlock()
	half, err := m.Select("g", "u", http.Header{}, "/v1/responses", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Select("a", "u", http.Header{}, "/v1/responses", nil); err == nil {
		t.Fatal("quota bypass via direct provider")
	}
	m.ReportSelection(half, nil, true)
	if _, err = m.Select("a", "u", http.Header{}, "/v1/responses", nil); err != nil {
		t.Fatal("canceled lease leaked")
	}
}
