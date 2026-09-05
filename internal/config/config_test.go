package config

import "testing"

import "time"

func TestLoadUsesDefaultAdminCredentials(t *testing.T) {
	t.Setenv("UNISUB_ADMIN_USERNAME", "")
	t.Setenv("UNISUB_ADMIN_PASSWORD", "")
	t.Setenv("UNISUB_GROK_OAUTH_ISSUER", "")
	t.Setenv("UNISUB_GROK_OAUTH_CLIENT_ID", "")
	t.Setenv("UNISUB_GROK_OAUTH_SCOPES", "")
	t.Setenv("UNISUB_GROK_CLIENT_VERSION", "")
	t.Setenv("UNISUB_GROK_BILLING_URL", "")
	t.Setenv("UNISUB_DB_DRIVER", "")
	t.Setenv("UNISUB_DB_DSN", "")
	t.Setenv("UNISUB_DB_PATH", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AdminUsername != "admin" || cfg.AdminPassword != "admin" {
		t.Fatalf("admin credentials = %q/%q", cfg.AdminUsername, cfg.AdminPassword)
	}
	if cfg.MaxRequestBodyBytes != 256<<20 {
		t.Fatalf("max request body bytes = %d", cfg.MaxRequestBodyBytes)
	}
	if cfg.ConcurrencyQueueTimeout != 3*time.Minute {
		t.Fatalf("concurrency queue timeout = %s", cfg.ConcurrencyQueueTimeout)
	}
	if cfg.RequestLogRetentionDays != 30 {
		t.Fatalf("request log retention = %d", cfg.RequestLogRetentionDays)
	}
	if cfg.GrokOAuth.Issuer != DefaultGrokOAuthIssuer || cfg.GrokOAuth.ClientID != DefaultGrokOAuthClientID {
		t.Fatalf("Grok OAuth defaults = %+v", cfg.GrokOAuth)
	}
	if len(cfg.GrokOAuth.Scopes) == 0 || cfg.GrokOAuth.ClientVersion != DefaultGrokOAuthClientVersion {
		t.Fatalf("Grok OAuth scopes/version = %+v", cfg.GrokOAuth)
	}
	if cfg.ClaudeOAuth.ClientID != DefaultClaudeOAuthClientID || cfg.ClaudeOAuth.AuthorizeURL != DefaultClaudeOAuthAuthorizeURL {
		t.Fatalf("Claude OAuth defaults = %+v", cfg.ClaudeOAuth)
	}
	if cfg.CodexOAuth.ClientID != DefaultCodexOAuthClientID || cfg.CodexOAuth.RedirectURI != DefaultCodexOAuthRedirectURI {
		t.Fatalf("Codex OAuth defaults = %+v", cfg.CodexOAuth)
	}
	if cfg.Providers.GrokBilling != DefaultGrokBillingURL {
		t.Fatalf("Grok billing URL = %q", cfg.Providers.GrokBilling)
	}
	if cfg.DatabaseDriver != "sqlite" || cfg.DatabaseDSN != "./data/unisub.db" || cfg.DatabasePath != "./data/unisub.db" {
		t.Fatalf("database defaults = %s %s %s", cfg.DatabaseDriver, cfg.DatabaseDSN, cfg.DatabasePath)
	}
}

func TestLoadUsesSQLitePathFromEnvironment(t *testing.T) {
	t.Setenv("UNISUB_DB_DRIVER", "sqlite")
	t.Setenv("UNISUB_DB_PATH", "./tmp/custom.db")
	t.Setenv("UNISUB_DB_DSN", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DatabaseDriver != "sqlite" || cfg.DatabaseDSN != "./tmp/custom.db" {
		t.Fatalf("sqlite config = %s %s", cfg.DatabaseDriver, cfg.DatabaseDSN)
	}
}

func TestLoadDetectsPostgresDSN(t *testing.T) {
	t.Setenv("UNISUB_DB_DRIVER", "")
	t.Setenv("UNISUB_DB_DSN", "postgres://unisub:unisub@127.0.0.1:5432/unisub?sslmode=disable")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DatabaseDriver != "postgres" {
		t.Fatalf("driver = %q", cfg.DatabaseDriver)
	}
}

func TestLoadRequiresPostgresDSN(t *testing.T) {
	t.Setenv("UNISUB_DB_DRIVER", "postgres")
	t.Setenv("UNISUB_DB_DSN", "")
	if _, err := Load(); err == nil {
		t.Fatal("postgres without DSN was accepted")
	}
}

func TestLoadRejectsUnknownDatabaseDriver(t *testing.T) {
	t.Setenv("UNISUB_DB_DRIVER", "mysql")
	if _, err := Load(); err == nil {
		t.Fatal("mysql driver was accepted")
	}
}

func TestLoadUsesConcurrencyQueueTimeoutFromEnvironment(t *testing.T) {
	t.Setenv("UNISUB_CONCURRENCY_QUEUE_TIMEOUT_SECONDS", "45")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConcurrencyQueueTimeout != 45*time.Second {
		t.Fatalf("concurrency queue timeout = %s", cfg.ConcurrencyQueueTimeout)
	}
}

func TestZeroConcurrencyQueueTimeoutUsesBuiltInDefault(t *testing.T) {
	t.Setenv("UNISUB_CONCURRENCY_QUEUE_TIMEOUT_SECONDS", "0")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConcurrencyQueueTimeout != 3*time.Minute {
		t.Fatalf("concurrency queue timeout = %s", cfg.ConcurrencyQueueTimeout)
	}
}

func TestLoadUsesRequestLogRetentionFromEnvironment(t *testing.T) {
	t.Setenv("UNISUB_REQUEST_LOG_RETENTION_DAYS", "45")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RequestLogRetentionDays != 45 {
		t.Fatalf("request log retention = %d", cfg.RequestLogRetentionDays)
	}
}

func TestLoadUsesAdminCredentialsFromEnvironment(t *testing.T) {
	t.Setenv("UNISUB_ADMIN_USERNAME", "operator")
	t.Setenv("UNISUB_ADMIN_PASSWORD", "environment-password")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AdminUsername != "operator" || cfg.AdminPassword != "environment-password" {
		t.Fatalf("admin credentials = %q/%q", cfg.AdminUsername, cfg.AdminPassword)
	}
}

func TestLoadUsesPortEnvironment(t *testing.T) {
	t.Setenv("UNISUB_LISTEN", "")
	t.Setenv("UNISUB_PORT", "9090")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddress != ":9090" {
		t.Fatalf("listen address = %q", cfg.ListenAddress)
	}
}

func TestListenEnvironmentTakesPriorityOverPort(t *testing.T) {
	t.Setenv("UNISUB_LISTEN", "127.0.0.1:9191")
	t.Setenv("UNISUB_PORT", "9090")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddress != "127.0.0.1:9191" {
		t.Fatalf("listen address = %q", cfg.ListenAddress)
	}
}

func TestGrokOAuthScopesAcceptCommaOrSpaceSeparatedValues(t *testing.T) {
	t.Setenv("UNISUB_GROK_OAUTH_SCOPES", "openid,offline_access grok-cli:access")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(cfg.GrokOAuth.Scopes); got != 3 {
		t.Fatalf("scope count = %d (%v)", got, cfg.GrokOAuth.Scopes)
	}
}

func TestProductionRejectsNonOfficialGrokOAuthIssuer(t *testing.T) {
	t.Setenv("UNISUB_ALLOW_TEST_UPSTREAMS", "false")
	t.Setenv("UNISUB_GROK_OAUTH_ISSUER", "https://example.com")
	if _, err := Load(); err == nil {
		t.Fatal("non-official Grok OAuth issuer was accepted")
	}
}

func TestProductionRejectsNonOfficialClaudeOAuthURLs(t *testing.T) {
	t.Setenv("UNISUB_ALLOW_TEST_UPSTREAMS", "false")
	t.Setenv("UNISUB_CLAUDE_OAUTH_TOKEN_URL", "https://example.com/token")
	if _, err := Load(); err == nil {
		t.Fatal("non-official Claude OAuth token URL was accepted")
	}
}

func TestProductionRejectsNonOfficialCodexOAuthURLs(t *testing.T) {
	t.Setenv("UNISUB_ALLOW_TEST_UPSTREAMS", "false")
	t.Setenv("UNISUB_CODEX_OAUTH_REDIRECT_URI", "https://example.com/callback")
	if _, err := Load(); err == nil {
		t.Fatal("non-official Codex OAuth redirect URI was accepted")
	}
}
