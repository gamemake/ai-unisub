package database

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCallQueryMatchesUserByKeyValueAndLegacyID(t *testing.T) {
	db, err := NewDatabase("sqlite::memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Open(); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SaveAccount(&PersistedAccount{ID: "provider", Name: "test", AIProvider: "dummy", Config: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	for _, user := range []PersistedUser{{ID: "a", Name: "alice", Enabled: true}, {ID: "b", Name: "bob", Enabled: true}} {
		if err = db.SaveUser(&user); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []PersistedAPIKey{{ID: "key-a", Key: "secret-a", Name: "a", UserID: "a", AccountID: "provider"}, {ID: "key-b", Key: "secret-b", Name: "b", UserID: "b", AccountID: "provider"}} {
		if err = db.SaveAPIKey(&key); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	for _, trace := range []PersistedCallTrace{{ID: "alice-value", APIKey: "secret-a"}, {ID: "alice-legacy", APIKey: "key-a"}, {ID: "bob-value", APIKey: "secret-b"}} {
		trace.StartedAt = now
		trace.FinishedAt = now
		if err = db.RecordCallTrace(&trace); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name  string
		count int
	}{{"alice", 2}, {"bob", 1}, {"unknown", 0}} {
		rows, count, err := db.QueryCallTraces(test.name, "", nil, 1, 10, nil)
		if err != nil || count != test.count || len(rows) != test.count {
			t.Fatalf("user=%s count=%d rows=%d err=%v", test.name, count, len(rows), err)
		}
	}
}
