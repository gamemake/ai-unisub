package oauth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFileCredentialStoreRoundTripAndAtomicReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "credential.json")
	store := FileCredentialStore{}
	value := json.RawMessage(`{"service":"claude","access_token":"secret"}`)
	if err := store.SaveCredential(path, value); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadCredential(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(value) {
		t.Fatalf("got=%s", got)
	}
	if err := store.SaveCredential(path, json.RawMessage(`{"service":"codex"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
