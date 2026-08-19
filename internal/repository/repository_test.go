package repository

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ai-unisub/ai-unisub/internal/cryptox"
	"github.com/ai-unisub/ai-unisub/internal/database"
	"github.com/ai-unisub/ai-unisub/internal/model"
)

func testRepository(t *testing.T) *Repository {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := cryptox.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo := New(db, cipher, "test-v1")
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

func TestAccountAPIKeyLifecycle(t *testing.T) {
	ctx := context.Background()
	repo := testRepository(t)
	created, err := repo.BootstrapAdmin(ctx, "admin", "correct horse battery staple")
	if err != nil || !created {
		t.Fatalf("bootstrap: created=%v err=%v", created, err)
	}
	if err := repo.AuthenticateAdmin(ctx, "admin", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}

	account, key, err := repo.CreateAccount(ctx, CreateAccountParams{
		Name: "codex-one", Provider: model.ProviderCodex, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "upstream-secret", ChatGPTAccountID: "acct-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if key == "" || account.APIKeyPrefix == "" {
		t.Fatal("account key was not returned")
	}
	resolved, err := repo.ResolveAPIKey(ctx, key)
	if err != nil || resolved.ID != account.ID {
		t.Fatalf("resolve: account=%+v err=%v", resolved, err)
	}
	credentials, err := repo.Credentials(ctx, resolved.Account)
	if err != nil || credentials.AccessToken != "upstream-secret" {
		t.Fatalf("credentials: %+v err=%v", credentials, err)
	}

	newKey, err := repo.ResetAPIKey(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ResolveAPIKey(ctx, key); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old key still valid: %v", err)
	}
	if _, err := repo.ResolveAPIKey(ctx, newKey); err != nil {
		t.Fatalf("new key invalid: %v", err)
	}
	if err := repo.SetAccountEnabled(ctx, account.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ResolveAPIKey(ctx, newKey); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("disabled account resolved: %v", err)
	}
}
