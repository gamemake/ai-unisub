package service

import (
	"ai-unisub/internal/oauth"
	"testing"
	"time"
)

func TestOAuthSessionResultOwnershipExpiryAndConsumption(t *testing.T) {
	store := NewOAuthResultStore()
	id, err := store.Put(OAuthResult{SessionID: "session", SubjectID: "alice", Service: "openai", Credential: oauth.OAuthCredential{AccessToken: "token"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.FindSession("session", "openai", "bob"); ok {
		t.Fatal("result visible to another user")
	}
	if _, ok := store.FindSession("session", "claude", "alice"); ok {
		t.Fatal("result visible to another service")
	}
	if got, ok := store.FindSession("session", "openai", "alice"); !ok || got != id {
		t.Fatal("owner could not find result")
	}
	if _, err := store.Take(id, "bob"); err == nil {
		t.Fatal("other user consumed result")
	}
	if value, err := store.Take(id, "alice"); err != nil || value.Credential.AccessToken != "token" {
		t.Fatal("owner could not consume result")
	}
	if _, ok := store.FindSession("session", "openai", "alice"); ok {
		t.Fatal("consumed result remains visible")
	}
	store.ttl = -time.Second
	id, _ = store.Put(OAuthResult{SessionID: "expired", SubjectID: "alice", Service: "codex"})
	if _, ok := store.FindSession("expired", "codex", "alice"); ok {
		t.Fatal("expired result remains visible")
	}
	if _, err := store.Take(id, "alice"); err == nil {
		t.Fatal("expired result consumed")
	}
}
