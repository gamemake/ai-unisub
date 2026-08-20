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

	account, err := repo.CreateAccount(ctx, CreateAccountParams{
		Name: "codex-one", Provider: model.ProviderCodex, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "upstream-secret", ChatGPTAccountID: "acct-1"},
		ProxyURL:    "socks5://proxy-user:proxy-pass@127.0.0.1:1080", ConcurrencyQueueTimeoutSeconds: 15,
	})
	if err != nil {
		t.Fatal(err)
	}
	createdKey, key, err := repo.CreateAPIKey(ctx, CreateAPIKeyParams{AccountID: account.ID, Name: "codex-one"})
	if err != nil {
		t.Fatal(err)
	}
	if key == "" || createdKey.KeyPrefix == "" || createdKey.AccountID != account.ID {
		t.Fatal("account key was not returned")
	}
	account, err = repo.GetAccount(ctx, account.ID)
	if err != nil || account.APIKeyCount != 1 {
		t.Fatalf("key count = %+v err=%v", account, err)
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

	newKey, err := repo.ResetAPIKey(ctx, createdKey.ID)
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

func TestUpdateAccountSettings(t *testing.T) {
	ctx := context.Background()
	repo := testRepository(t)
	account, err := repo.CreateAccount(ctx, CreateAccountParams{
		Name: "edit-me", Provider: model.ProviderGrok, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "token"},
		ConcurrencyLimit: 1, ProxyURL: "http://127.0.0.1:8080",
	})
	if err != nil {
		t.Fatal(err)
	}
	cleared := ""
	updated, err := repo.UpdateAccount(ctx, account.ID, UpdateAccountParams{
		Name: "edited", Enabled: false, ConcurrencyLimit: 4,
		ConcurrencyQueueTimeoutSeconds: 20, ProxyURL: &cleared,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "edited" || updated.Enabled || updated.ConcurrencyLimit != 4 || updated.ConcurrencyQueueTimeoutSeconds != 20 {
		t.Fatalf("updated account = %+v", updated)
	}
	if updated.ProxyConfigured {
		t.Fatalf("proxy was not cleared: %+v", updated)
	}
	kept, err := repo.UpdateAccount(ctx, account.ID, UpdateAccountParams{
		Name: "edited-again", Enabled: true, ConcurrencyLimit: 2, ConcurrencyQueueTimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if kept.Name != "edited-again" || !kept.Enabled || kept.ConcurrencyLimit != 2 {
		t.Fatalf("kept proxy update = %+v", kept)
	}
	if kept.ProxyConfigured {
		t.Fatal("cleared proxy came back")
	}
}

func TestMultipleAPIKeysCanBindToOneAccount(t *testing.T) {
	ctx := context.Background()
	repo := testRepository(t)
	account, err := repo.CreateAccount(ctx, CreateAccountParams{
		Name: "shared", Provider: model.ProviderGrok, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, firstPlain, err := repo.CreateAPIKey(ctx, CreateAPIKeyParams{AccountID: account.ID, Name: "first"})
	if err != nil {
		t.Fatal(err)
	}
	second, secondPlain, err := repo.CreateAPIKey(ctx, CreateAPIKeyParams{AccountID: account.ID, Name: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || firstPlain == secondPlain {
		t.Fatal("keys were not unique")
	}
	resolvedFirst, err := repo.ResolveAPIKey(ctx, firstPlain)
	if err != nil || resolvedFirst.ID != account.ID || resolvedFirst.APIKeyID != first.ID {
		t.Fatalf("first key resolve = %+v err=%v", resolvedFirst, err)
	}
	resolvedSecond, err := repo.ResolveAPIKey(ctx, secondPlain)
	if err != nil || resolvedSecond.APIKeyID != second.ID {
		t.Fatalf("second key resolve = %+v err=%v", resolvedSecond, err)
	}
	listed, err := repo.ListAPIKeys(ctx)
	if err != nil || len(listed) != 2 {
		t.Fatalf("list keys = %+v err=%v", listed, err)
	}
	account, err = repo.GetAccount(ctx, account.ID)
	if err != nil || account.APIKeyCount != 2 {
		t.Fatalf("key count = %+v err=%v", account, err)
	}
	if err := repo.DeleteAPIKey(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ResolveAPIKey(ctx, firstPlain); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("deleted key still resolved: %v", err)
	}
	if _, err := repo.ResolveAPIKey(ctx, secondPlain); err != nil {
		t.Fatalf("remaining key invalid: %v", err)
	}
}
