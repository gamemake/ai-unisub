package database

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCallTraceFiltersAndSessionMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.db")
	db := NewSQLiteDatabase(path)
	if err := db.Open(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	legacyDay := now.Add(-48 * time.Hour)
	table := traceTable(legacyDay)
	// Simulate an existing daily table created before session_id was introduced.
	legacy := strings.Replace(createTraceTableSQL(table), "session_id TEXT NOT NULL DEFAULT '', ", "", 1)
	if _, err := db.db.Exec(legacy); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = NewSQLiteDatabase(path)
	if err := db.Open(); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SaveUser(&PersistedUser{ID: "alice", Name: "alice"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveUser(&PersistedUser{ID: "bob", Name: "bob"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"account-a", "account-b"} {
		if err := db.SaveAccount(&PersistedAccount{ID: id, Name: id, AIProvider: "dummy", Config: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []PersistedAPIKey{{ID: "ka", Key: "secret-a", UserID: "alice", AccountID: "account-a"}, {ID: "kb", Key: "secret-b", UserID: "bob", AccountID: "account-b"}} {
		if err := db.SaveAPIKey(&key); err != nil {
			t.Fatal(err)
		}
	}
	traces := []PersistedCallTrace{
		{ID: "a", APIKey: "secret-a", AccountID: "account-a", SessionID: "session-a", RequestID: "request-a", SourceIP: "192.0.2.1", Model: "model-a", StartedAt: now, FinishedAt: now},
		{ID: "b", APIKey: "secret-b", AccountID: "account-b", SessionID: "session-b", RequestID: "request-b", SourceIP: "192.0.2.2", Model: "model-b", HTTPErrorCode: 500, StartedAt: now, FinishedAt: now},
		{ID: "old", APIKey: "ka", AccountID: "account-a", HTTPErrorInfo: "connection failed", StartedAt: legacyDay, FinishedAt: legacyDay},
	}
	for _, trace := range traces {
		if err := db.RecordCallTrace(&trace); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name   string
		filter CallTraceFilter
		count  int
	}{
		{"session", CallTraceFilter{Search: "session-a"}, 1}, {"request", CallTraceFilter{Search: "request-b"}, 1},
		{"ip", CallTraceFilter{Search: "192.0.2.1"}, 1}, {"model", CallTraceFilter{Search: "model-b"}, 1},
		{"no substring", CallTraceFilter{Search: "session"}, 0}, {"no wildcard", CallTraceFilter{Search: "%"}, 0},
		{"admin username", CallTraceFilter{Search: "alice", SearchUsernames: true}, 2}, {"member username disabled", CallTraceFilter{Search: "alice", UserName: "alice"}, 0},
		{"member isolation", CallTraceFilter{UserName: "alice", Search: "session-b"}, 0},
		{"account", CallTraceFilter{AccountID: "account-a"}, 2}, {"success", CallTraceFilter{Code: new(0)}, 2}, {"failure", CallTraceFilter{Code: new(500)}, 1},
		{"combined", CallTraceFilter{Search: "alice", SearchUsernames: true, AccountID: "account-a", Code: new(0), TimeRange: &TimeRange{Start: now.Add(-time.Hour), End: now}}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, total, err := db.QueryCallTracesFiltered(tt.filter, 1, 10)
			if err != nil || total != tt.count || len(rows) != tt.count {
				t.Fatalf("rows=%d total=%d err=%v", len(rows), total, err)
			}
		})
	}
	rows, total, err := db.QueryCallTracesFiltered(CallTraceFilter{}, 2, 1)
	if err != nil || total != 3 || len(rows) != 1 {
		t.Fatal("pagination", err)
	}
	detail, err := db.GetCallTrace(now, "a")
	if err != nil || detail.SessionID != "session-a" {
		t.Fatalf("detail session: %+v %v", detail, err)
	}
	rows, _, err = db.QueryCallTraces("", "", nil, 1, 10, nil, "session-a")
	if err != nil || len(rows) != 1 || rows[0].SessionID != "session-a" {
		t.Fatal("summary session", err)
	}
	old, err := db.GetCallTrace(legacyDay, "old")
	if err != nil || old.SessionID != "" {
		t.Fatal("old trace without session", err)
	}
}
