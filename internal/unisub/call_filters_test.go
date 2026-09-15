package unisub

import (
	"ai-unisub/internal/database"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestCallSessionID(t *testing.T) {
	for _, name := range []string{"Session-Id", "X-Session-Id", "session_id"} {
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
	var aliceID, bobID string
	for _, u := range users {
		if u.Name == "alice-filter" {
			aliceID = u.ID
		}
		if u.Name == "bob-filter" {
			bobID = u.ID
		}
	}
	for _, id := range []string{"account-a", "account-b"} {
		if err := s.Database().SaveAccount(&database.PersistedAccount{ID: id, Name: id, AIProvider: "dummy", Config: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []database.PersistedAPIKey{{ID: "ka", Key: "secret-a", UserID: aliceID, AccountID: "account-a"}, {ID: "kb", Key: "secret-b", UserID: bobID, AccountID: "account-b"}} {
		if err := s.Database().SaveAPIKey(&key); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	for _, trace := range []database.PersistedCallTrace{{ID: "a", APIKey: "secret-a", AccountID: "account-a", SessionID: "shared", StartedAt: now, FinishedAt: now}, {ID: "b", APIKey: "secret-b", AccountID: "account-b", SessionID: "shared", HTTPErrorCode: 500, StartedAt: now, FinishedAt: now}} {
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
	}{{"q=alice-filter", admin, 1}, {"q=alice-filter", member, 0}, {"q=shared&account_id=account-a&code=0&range=1d", admin, 1}, {"q=shared", member, 1}, {"q=bob-filter&search_usernames=true", member, 0}, {"account_id=account-b", member, 0}, {"code=500", admin, 1}, {"code=429", admin, 0}, {"code=500", member, 0}} {
		out := appRequest(s, "GET", "/api/calls?"+tt.query, "", tt.cookie)
		var result struct {
			Items []database.PersistedCallTraceSummary `json:"items"`
			Total int                                  `json:"total"`
		}
		if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &result) != nil || result.Total != tt.count {
			t.Fatalf("query=%s status=%d body=%s", tt.query, out.Code, out.Body.String())
		}
		for _, item := range result.Items {
			if item.APIKey != "" || item.SessionID != "shared" {
				t.Fatal("incorrect summary", item)
			}
		}
	}
	for _, q := range []string{"code=invalid", "code=-1", "code=99", "code=600", "code=500suffix", "code=5.0", "range=invalid", "range=custom&from=2026-09-15&to=2026-09-01", "range=custom"} {
		if out := appRequest(s, "GET", "/api/calls?"+q, "", admin); out.Code != 400 {
			t.Fatal(q, out.Code)
		}
	}
}
