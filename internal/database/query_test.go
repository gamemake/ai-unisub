package database

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testDB(t *testing.T) Database {
	t.Helper()
	db, err := NewDatabase("sqlite::memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func testFileDB(t *testing.T) (*MemoryDatabase, *SQLiteDatabase) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "calls.db")
	store := NewSQLiteDatabase(path)
	db := newMemoryDatabase(store)
	if err := db.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, store
}

func TestQueryCallTracesRequiresTimeRange(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	if _, _, err := db.QueryCallTraces(CallTraceFilter{}, 1, 10); err == nil {
		t.Fatal("empty time range was accepted")
	}
	if _, _, err := db.QueryCallTraces(CallTraceFilter{TimeRange: TimeRange{Start: now}}, 1, 10); err == nil {
		t.Fatal("missing end time was accepted")
	}
	if _, _, err := db.QueryCallTraces(CallTraceFilter{TimeRange: TimeRange{Start: now, End: now.Add(-time.Hour)}}, 1, 10); err == nil {
		t.Fatal("start after end was accepted")
	}
	if _, _, err := db.QueryCallTraces(CallTraceFilter{TimeRange: TimeRange{Start: now.Add(-time.Hour), End: now}}, 1, 10); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-400 * 24 * time.Hour)
	if _, _, err := db.QueryCallTraces(CallTraceFilter{TimeRange: TimeRange{Start: old, End: old.Add(24 * time.Hour)}}, 1, 10); err != nil {
		t.Fatal(err)
	}
}

func TestCallTraceExecutionMetadataRoundTrip(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	trace := &PersistedCallTrace{
		UserID:            7,
		APIKey:            "key",
		AIProviderType:    "dummy",
		AccountID:         1,
		RequestMethod:     "POST",
		URL:               "/v1/messages",
		QueueDurationMs:   12,
		RequestDurationMs: 34,
		FinishedAt:        now,
	}
	if err := db.RecordCallTrace(trace); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetCallTrace(now, trace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != 7 || got.RequestMethod != "POST" || got.QueueDurationMs != 12 || got.RequestDurationMs != 34 {
		t.Fatalf("metadata did not round trip: %+v", got)
	}
}

func TestNewCallTraceTableDoesNotPersistStartedAt(t *testing.T) {
	db, store := testFileDB(t)
	now := time.Now().UTC()
	if err := db.RecordCallTrace(&PersistedCallTrace{FinishedAt: now}); err != nil {
		t.Fatal(err)
	}
	columns, err := store.tableColumns(traceTable(now))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := columns["started_at"]; ok {
		t.Fatal("new call-trace schema still persists started_at")
	}
	if _, ok := columns["finished_at"]; !ok {
		t.Fatal("new call-trace schema is missing finished_at")
	}
}

func TestNewPostgreSQLCallTraceSchemaUsesFinishedAtPartitionKey(t *testing.T) {
	for _, statement := range postgresSchema {
		if !strings.Contains(statement, "CREATE TABLE IF NOT EXISTS call_traces ") {
			continue
		}
		if strings.Contains(statement, "started_at") || !strings.Contains(statement, "PARTITION BY RANGE (finished_at)") {
			t.Fatalf("unexpected PostgreSQL call-trace schema: %s", statement)
		}
	}
}

func TestQueryProxyLogsRequiresTimeRange(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	if _, _, err := db.QueryProxyLogs(ProxyLogFilter{GroupID: 1}, 1, 10); err == nil {
		t.Fatal("empty time range was accepted")
	}
	if _, _, err := db.QueryProxyLogs(ProxyLogFilter{GroupID: 1, TimeRange: TimeRange{Start: now.Add(-time.Hour), End: now}}, 1, 10); err != nil {
		t.Fatal(err)
	}
}

func TestCallTraceOutboundURLAndLegacySchema(t *testing.T) {
	db, store := testFileDB(t)
	now := time.Now().UTC()
	table := traceTable(now)

	// Seed an older daily table without outbound_url (and without session_id).
	legacySQL := `CREATE TABLE ` + table + ` (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		apikey TEXT, provider_type TEXT, account_id INTEGER, request_id TEXT,
		source_ip TEXT, url TEXT, http_error_code INTEGER, http_error_info TEXT,
		original_request_headers BLOB, outbound_request_headers BLOB,
		request_body BLOB, response_headers BLOB, response_body BLOB,
		request_bytes INTEGER NOT NULL DEFAULT 0, response_bytes INTEGER NOT NULL DEFAULT 0,
		model TEXT, input_tokens INTEGER, output_tokens INTEGER,
		cache_creation_tokens INTEGER, cache_read_tokens INTEGER,
		started_at DATETIME, finished_at DATETIME
	)`
	if _, err := store.db.Exec(legacySQL); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(
		`INSERT INTO `+table+`(apikey, provider_type, account_id, request_id, source_ip, url, http_error_code, http_error_info, original_request_headers, outbound_request_headers, response_headers, model, input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, started_at, finished_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"sk-legacy", "claude", 1, "req-legacy", "127.0.0.1", "/v1/messages", 200, "",
		[]byte(`{}`), []byte(`{}`), []byte(`{}`), "claude-sonnet", 1, 2, 0, 0, now, now,
	); err != nil {
		t.Fatal(err)
	}

	// Reading old rows must upgrade schema and return empty outbound_url.
	got, err := db.GetCallTrace(now, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "/v1/messages" || got.OutboundURL != "" || got.SessionID != "" {
		t.Fatalf("legacy detail: %#v", got)
	}

	// New writes fill outbound_url on the upgraded table.
	if err := db.RecordCallTrace(&PersistedCallTrace{
		APIKey: "sk-new", AIProviderType: "claude", AccountID: 1, RequestID: "req-new",
		URL: "/v1/messages", OutboundURL: "https://api.anthropic.com/v1/messages",
		HTTPErrorCode: 201, Model: "claude-sonnet", InputTokens: 3, OutputTokens: 4,
		FinishedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	items, total, err := db.QueryCallTraces(CallTraceFilter{TimeRange: TimeRange{Start: now.Add(-time.Hour), End: now.Add(time.Hour)}}, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("total=%d items=%#v", total, items)
	}
	var foundNew bool
	for _, item := range items {
		if item.RequestID == "req-new" {
			foundNew = true
			if item.OutboundURL != "https://api.anthropic.com/v1/messages" || item.URL != "/v1/messages" {
				t.Fatalf("new summary: %#v", item)
			}
		}
		if item.RequestID == "req-legacy" && item.OutboundURL != "" {
			t.Fatalf("legacy summary should keep empty outbound_url: %#v", item)
		}
	}
	if !foundNew {
		t.Fatalf("missing new row: %#v", items)
	}
	detail, err := db.GetCallTrace(now, items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.RequestID == "req-new" && detail.OutboundURL != "https://api.anthropic.com/v1/messages" {
		t.Fatalf("new detail: %#v", detail)
	}
}
