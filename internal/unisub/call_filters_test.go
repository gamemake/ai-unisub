package unisub

import (
	"ai-unisub/internal/database"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestCallSessionID(t *testing.T) {
	for _, name := range []string{"Session-Id", "X-Session-Id", "session-id", "session_id"} {
		h := http.Header{}
		h.Set("User-Agent", "codex-tui/1.0")
		h.Set(name, "header-session")
		if got := callSessionID(h); got != "header-session" {
			t.Fatal(name, got)
		}
		h.Set("User-Agent", "curl/8.0")
		if got := callSessionID(h); got != "" {
			t.Fatal("unmatched client must have empty session", got)
		}
	}
	if got := callSessionID(nil); got != "" {
		t.Fatal(got)
	}
	if got := callSessionID(http.Header{"X-Unisub-Session-Id": {"ignored"}}); got != "" {
		t.Fatal("removed override was read")
	}
}
func TestCallsAPICombinedFiltersAndAuthorization(t *testing.T) {
	s := testApp(t)
	admin := loginTestApp(t, s)
	for _, body := range []string{`{"name":"alice-filter","password":"password-123","role":"user"}`, `{"name":"bob-filter","password":"password-123","role":"user"}`} {
		if out := appRequest(s, "POST", "/api/users", body, admin); out.Code != 201 {
			t.Fatal(out.Body.String())
		}
	}
	users, _ := s.Database().ListUsers()
	var aliceID, bobID int
	for _, u := range users {
		if u.Name == "alice-filter" {
			aliceID = u.ID
		}
		if u.Name == "bob-filter" {
			bobID = u.ID
		}
	}
	accountA := &database.PersistedAccount{Name: "account-a", AIProvider: "dummy", Config: json.RawMessage(`{}`)}
	accountB := &database.PersistedAccount{Name: "account-b", AIProvider: "dummy", Config: json.RawMessage(`{}`)}
	if err := s.Database().SaveAccount(accountA); err != nil {
		t.Fatal(err)
	}
	if err := s.Database().SaveAccount(accountB); err != nil {
		t.Fatal(err)
	}
	for _, key := range []database.PersistedAPIKey{{Key: "secret-a", UserID: aliceID, AccountID: accountA.ID}, {Key: "secret-b", UserID: bobID, AccountID: accountB.ID}} {
		if err := s.Database().SaveAPIKey(&key); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	for _, trace := range []database.PersistedCallTrace{{APIKey: "secret-a", AccountID: accountA.ID, SessionID: "shared", StartedAt: now, FinishedAt: now}, {APIKey: "secret-b", AccountID: accountB.ID, SessionID: "shared", HTTPErrorCode: 500, StartedAt: now, FinishedAt: now}, {APIKey: "unknown", AccountID: accountA.ID, SessionID: "unattributed", StartedAt: now, FinishedAt: now}} {
		if err := s.Database().RecordCallTrace(&trace); err != nil {
			t.Fatal(err)
		}
	}
	login := appRequest(s, "POST", "/api/login", `{"username":"alice-filter","password":"password-123"}`, nil)
	member := login.Result().Cookies()[0]
	for _, tt := range []struct {
		query  string
		cookie *http.Cookie
		count  int
	}{{"q=alice-filter", admin, 1}, {"q=alice-filter", member, 0}, {"q=none", admin, 1}, {"q=none", member, 0}, {"q=shared&account_id=" + pathID(accountA.ID) + "&code=0&range=1d", admin, 1}, {"q=shared", member, 1}, {"q=bob-filter&search_usernames=true", member, 0}, {"account_id=" + pathID(accountB.ID), member, 0}, {"code=500", admin, 1}, {"code=429", admin, 0}, {"code=500", member, 0}} {
		out := appRequest(s, "GET", "/api/calls?"+tt.query, "", tt.cookie)
		var result struct {
			Items []database.PersistedCallTraceSummary `json:"items"`
			Total int                                  `json:"total"`
		}
		if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &result) != nil || result.Total != tt.count {
			t.Fatalf("query=%s status=%d body=%s", tt.query, out.Code, out.Body.String())
		}
		for _, item := range result.Items {
			validSession := item.SessionID == "shared" || (tt.query == "q=none" && item.SessionID == "unattributed")
			if item.APIKey != "" || !validSession {
				t.Fatal("incorrect summary", item)
			}
			if tt.query == "q=alice-filter" && item.Username != "alice-filter" {
				t.Fatalf("expected username in call summary, got %q", item.Username)
			}
		}
	}
	for _, q := range []string{"code=invalid", "code=-1", "code=99", "code=600", "code=500suffix", "code=5.0", "range=invalid", "range=custom&from=2026-09-15&to=2026-09-01", "range=custom"} {
		if out := appRequest(s, "GET", "/api/calls?"+q, "", admin); out.Code != 400 {
			t.Fatal(q, out.Code)
		}
	}
}
