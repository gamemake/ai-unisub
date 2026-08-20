package repository

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ai-unisub/ai-unisub/internal/database"
	"github.com/ai-unisub/ai-unisub/internal/model"
)

func testRepository(t *testing.T) *Repository {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	repo := New(db)
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
		ProxyURL:    "socks5://proxy-user:proxy-pass@127.0.0.1:1080", ConcurrencyQueueTimeoutSeconds: 15,
	})
	if err != nil {
		t.Fatal(err)
	}
	if key == "" || account.APIKeyPrefix == "" {
		t.Fatal("account key was not returned")
	}
	if !account.ProxyConfigured || account.ProxyURL == "" {
		t.Fatal("account proxy was not stored")
	}
	var storedCredentials []byte
	var storedProxy string
	if err := repo.db.QueryRowContext(ctx, `SELECT credentials_json, proxy_url FROM accounts WHERE id=?`, account.ID).Scan(&storedCredentials, &storedProxy); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(storedCredentials, []byte("upstream-secret")) || storedProxy != "socks5://proxy-user:proxy-pass@127.0.0.1:1080" {
		t.Fatalf("account secrets were not stored as plaintext: credentials=%s proxy=%q", storedCredentials, storedProxy)
	}
	if account.ConcurrencyQueueTimeoutSeconds != 15 {
		t.Fatalf("queue timeout = %d", account.ConcurrencyQueueTimeoutSeconds)
	}
	resolved, err := repo.ResolveAPIKey(ctx, key)
	if err != nil || resolved.ID != account.ID {
		t.Fatalf("resolve: account=%+v err=%v", resolved, err)
	}
	credentials, err := repo.Credentials(ctx, resolved.Account)
	if err != nil || credentials.AccessToken != "upstream-secret" {
		t.Fatalf("credentials: %+v err=%v", credentials, err)
	}
	proxyURL, err := repo.ProxyURL(resolved.Account)
	if err != nil || proxyURL != "socks5://proxy-user:proxy-pass@127.0.0.1:1080" {
		t.Fatalf("proxy URL: %q err=%v", proxyURL, err)
	}
	if err := repo.SetAccountProxy(ctx, account.ID, "http://127.0.0.1:3128"); err != nil {
		t.Fatal(err)
	}
	updated, err := repo.GetAccount(ctx, account.ID)
	if err != nil || !updated.ProxyConfigured {
		t.Fatalf("updated proxy state: %+v err=%v", updated, err)
	}
	proxyURL, err = repo.ProxyURL(updated)
	if err != nil || proxyURL != "http://127.0.0.1:3128" {
		t.Fatalf("updated proxy URL: %q err=%v", proxyURL, err)
	}
	if err := repo.SetAccountProxy(ctx, account.ID, ""); err != nil {
		t.Fatal(err)
	}
	updated, err = repo.GetAccount(ctx, account.ID)
	if err != nil || updated.ProxyConfigured {
		t.Fatalf("cleared proxy state: %+v err=%v", updated, err)
	}
	if err := repo.SetConcurrencyQueueTimeout(ctx, account.ID, 25); err != nil {
		t.Fatal(err)
	}
	updated, err = repo.GetAccount(ctx, account.ID)
	if err != nil || updated.ConcurrencyQueueTimeoutSeconds != 25 {
		t.Fatalf("updated queue timeout: %+v err=%v", updated, err)
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
