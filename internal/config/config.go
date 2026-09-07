package config

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type ProviderURLs struct {
	ClaudeAPI    string
	ClaudeModels string
	ClaudeUsage  string
	CodexAPI     string
	CodexModels  string
	CodexUsage   string
	GrokAPI      string
	GrokModels   string
	GrokBilling  string
}

type GrokOAuthConfig struct {
	Issuer        string
	ClientID      string
	Scopes        []string
	ClientVersion string
}

type ClaudeOAuthConfig struct {
	AuthorizeURL string
	TokenURL     string
	RedirectURI  string
	ClientID     string
	Scopes       []string
}

type CodexOAuthConfig struct {
	AuthorizeURL string
	TokenURL     string
	RedirectURI  string
	ClientID     string
	Scopes       []string
}

const (
	DefaultGrokOAuthIssuer        = "https://auth.x.ai"
	DefaultGrokOAuthClientID      = "b1a00492-073a-47ea-816f-4c329264a828"
	DefaultGrokOAuthClientVersion = "1.0.6"
	DefaultGrokBillingURL         = "https://cli-chat-proxy.grok.com/v1/billing?format=credits"
	DefaultClaudeUsageURL         = "https://api.anthropic.com/api/oauth/usage"
	DefaultCodexUsageURL          = "https://chatgpt.com/backend-api/wham/usage"

	// Claude Code public OAuth client (v2.1.81+ uses platform.claude.com).
	DefaultClaudeOAuthAuthorizeURL = "https://claude.com/cai/oauth/authorize"
	DefaultClaudeOAuthTokenURL     = "https://platform.claude.com/v1/oauth/token"
	DefaultClaudeOAuthRedirectURI  = "https://platform.claude.com/oauth/code/callback"
	DefaultClaudeOAuthClientID     = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

	DefaultCodexOAuthAuthorizeURL = "https://auth.openai.com/oauth/authorize"
	DefaultCodexOAuthTokenURL     = "https://auth.openai.com/oauth/token"
	DefaultCodexOAuthRedirectURI  = "http://localhost:1455/auth/callback"
	DefaultCodexOAuthClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
)

var DefaultGrokOAuthScopes = []string{
	"openid", "profile", "email", "offline_access", "grok-cli:access", "api:access",
	"conversations:read", "conversations:write", "workspaces:read", "workspaces:write",
}

var DefaultClaudeOAuthScopes = []string{
	"org:create_api_key", "user:profile", "user:inference",
	"user:sessions:claude_code", "user:mcp_servers", "user:file_upload",
}

var DefaultCodexOAuthScopes = []string{
	"openid", "profile", "email", "offline_access",
}

const DefaultConcurrencyQueueTimeout = 3 * time.Minute

type Config struct {
	ListenAddress           string
	DatabaseDriver          string
	DatabaseDSN             string
	DatabasePath            string
	AdminUsername           string
	AdminPassword           string
	AdminTokenTTL           time.Duration
	MaxRequestBodyBytes     int64
	ShutdownTimeout         time.Duration
	ConcurrencyQueueTimeout time.Duration
	RequestLogRetentionDays int
	AllowTestUpstreams      bool
	Providers               ProviderURLs
	GrokOAuth               GrokOAuthConfig
	ClaudeOAuth             ClaudeOAuthConfig
	CodexOAuth              CodexOAuthConfig
}

func Load() (Config, error) {
	driver, dsn, path, err := databaseSettings()
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		ListenAddress:           listenAddress(),
		DatabaseDriver:          driver,
		DatabaseDSN:             dsn,
		DatabasePath:            path,
		AdminUsername:           env("UNISUB_ADMIN_USERNAME", "admin"),
		AdminPassword:           env("UNISUB_ADMIN_PASSWORD", "admin"),
		AdminTokenTTL:           durationEnv("UNISUB_ADMIN_TOKEN_TTL", 8*time.Hour),
		MaxRequestBodyBytes:     int64Env("UNISUB_MAX_BODY_BYTES", 256<<20),
		ShutdownTimeout:         durationEnv("UNISUB_SHUTDOWN_TIMEOUT", 15*time.Second),
		ConcurrencyQueueTimeout: time.Duration(int64Env("UNISUB_CONCURRENCY_QUEUE_TIMEOUT_SECONDS", int64(DefaultConcurrencyQueueTimeout/time.Second))) * time.Second,
		RequestLogRetentionDays: int(int64Env("UNISUB_REQUEST_LOG_RETENTION_DAYS", 30)),
		AllowTestUpstreams:      boolEnv("UNISUB_ALLOW_TEST_UPSTREAMS", false),
		GrokOAuth: GrokOAuthConfig{
			Issuer:        env("UNISUB_GROK_OAUTH_ISSUER", DefaultGrokOAuthIssuer),
			ClientID:      env("UNISUB_GROK_OAUTH_CLIENT_ID", DefaultGrokOAuthClientID),
			Scopes:        fieldsEnv("UNISUB_GROK_OAUTH_SCOPES", DefaultGrokOAuthScopes),
			ClientVersion: env("UNISUB_GROK_CLIENT_VERSION", DefaultGrokOAuthClientVersion),
		},
		ClaudeOAuth: ClaudeOAuthConfig{
			AuthorizeURL: env("UNISUB_CLAUDE_OAUTH_AUTHORIZE_URL", DefaultClaudeOAuthAuthorizeURL),
			TokenURL:     env("UNISUB_CLAUDE_OAUTH_TOKEN_URL", DefaultClaudeOAuthTokenURL),
			RedirectURI:  env("UNISUB_CLAUDE_OAUTH_REDIRECT_URI", DefaultClaudeOAuthRedirectURI),
			ClientID:     env("UNISUB_CLAUDE_OAUTH_CLIENT_ID", DefaultClaudeOAuthClientID),
			Scopes:       fieldsEnv("UNISUB_CLAUDE_OAUTH_SCOPES", DefaultClaudeOAuthScopes),
		},
		CodexOAuth: CodexOAuthConfig{
			AuthorizeURL: env("UNISUB_CODEX_OAUTH_AUTHORIZE_URL", DefaultCodexOAuthAuthorizeURL),
			TokenURL:     env("UNISUB_CODEX_OAUTH_TOKEN_URL", DefaultCodexOAuthTokenURL),
			RedirectURI:  env("UNISUB_CODEX_OAUTH_REDIRECT_URI", DefaultCodexOAuthRedirectURI),
			ClientID:     env("UNISUB_CODEX_OAUTH_CLIENT_ID", DefaultCodexOAuthClientID),
			Scopes:       fieldsEnv("UNISUB_CODEX_OAUTH_SCOPES", DefaultCodexOAuthScopes),
		},
		Providers: ProviderURLs{
			ClaudeAPI:    env("UNISUB_CLAUDE_API_URL", "https://api.anthropic.com/v1"),
			ClaudeModels: env("UNISUB_CLAUDE_MODELS_URL", "https://api.anthropic.com/v1/models"),
			ClaudeUsage:  env("UNISUB_CLAUDE_USAGE_URL", DefaultClaudeUsageURL),
			CodexAPI:     env("UNISUB_CODEX_API_URL", "https://chatgpt.com/backend-api/codex/responses"),
			CodexModels:  env("UNISUB_CODEX_MODELS_URL", "https://chatgpt.com/backend-api/codex/models"),
			CodexUsage:   env("UNISUB_CODEX_USAGE_URL", DefaultCodexUsageURL),
			GrokAPI:      env("UNISUB_GROK_API_URL", "https://cli-chat-proxy.grok.com/v1/responses"),
			GrokModels:   env("UNISUB_GROK_MODELS_URL", "https://cli-chat-proxy.grok.com/v1/models"),
			GrokBilling:  env("UNISUB_GROK_BILLING_URL", DefaultGrokBillingURL),
		},
	}

	if cfg.MaxRequestBodyBytes < 1024 {
		return Config{}, errors.New("UNISUB_MAX_BODY_BYTES must be at least 1024")
	}
	if cfg.ConcurrencyQueueTimeout == 0 {
		cfg.ConcurrencyQueueTimeout = DefaultConcurrencyQueueTimeout
	}
	if cfg.ConcurrencyQueueTimeout < 0 || cfg.ConcurrencyQueueTimeout > 5*time.Minute {
		return Config{}, errors.New("UNISUB_CONCURRENCY_QUEUE_TIMEOUT_SECONDS must be between 0 and 300")
	}
	if cfg.RequestLogRetentionDays < 1 || cfg.RequestLogRetentionDays > 3650 {
		return Config{}, errors.New("UNISUB_REQUEST_LOG_RETENTION_DAYS must be between 1 and 3650")
	}
	if err := cfg.validateDatabase(); err != nil {
		return Config{}, err
	}
	if err := cfg.validateUpstreams(); err != nil {
		return Config{}, err
	}
	if err := cfg.validateGrokOAuth(); err != nil {
		return Config{}, err
	}
	if err := cfg.validateClaudeOAuth(); err != nil {
		return Config{}, err
	}
	if err := cfg.validateCodexOAuth(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) AdminSigningKey() []byte {
	sum := sha256.Sum256([]byte(c.AdminUsername + "\x00" + c.AdminPassword + "\x00admin-session-v1"))
	return sum[:]
}

func (c Config) validateDatabase() error {
	switch c.DatabaseDriver {
	case "sqlite", "postgres":
	default:
		return fmt.Errorf("UNISUB_DB_DRIVER must be sqlite or postgres")
	}
	if strings.TrimSpace(c.DatabaseDSN) == "" {
		if c.DatabaseDriver == "postgres" {
			return errors.New("UNISUB_DB_DSN is required when UNISUB_DB_DRIVER=postgres")
		}
		return errors.New("database DSN is required")
	}
	return nil
}

func databaseSettings() (driver, dsn, path string, err error) {
	path = env("UNISUB_DB_PATH", "./data/unisub.db")
	dsn = strings.TrimSpace(os.Getenv("UNISUB_DB_DSN"))
	rawDriver := strings.TrimSpace(os.Getenv("UNISUB_DB_DRIVER"))
	driver, err = normalizeDatabaseDriver(rawDriver, dsn)
	if err != nil {
		return "", "", "", err
	}
	if driver == "sqlite" && dsn == "" {
		dsn = path
	}
	if driver == "sqlite" {
		path = dsn
	}
	return driver, dsn, path, nil
}

func normalizeDatabaseDriver(rawDriver, dsn string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(rawDriver)) {
	case "sqlite", "sqlite3":
		return "sqlite", nil
	case "postgres", "postgresql", "pg":
		return "postgres", nil
	case "":
		if looksLikePostgresDSN(dsn) {
			return "postgres", nil
		}
		return "sqlite", nil
	default:
		return "", fmt.Errorf("UNISUB_DB_DRIVER must be sqlite or postgres")
	}
}

func looksLikePostgresDSN(dsn string) bool {
	value := strings.ToLower(strings.TrimSpace(dsn))
	if strings.HasPrefix(value, "postgres://") || strings.HasPrefix(value, "postgresql://") {
		return true
	}
	return strings.Contains(value, "host=") && (strings.Contains(value, "dbname=") || strings.Contains(value, "user="))
}

func (c Config) validateUpstreams() error {
	if c.AllowTestUpstreams {
		return nil
	}
	allowed := map[string][]string{
		"ClaudeAPI":    {"https://api.anthropic.com/"},
		"ClaudeModels": {"https://api.anthropic.com/"},
		"ClaudeUsage":  {"https://api.anthropic.com/"},
		"CodexAPI":     {"https://chatgpt.com/"},
		"CodexModels":  {"https://chatgpt.com/"},
		"CodexUsage":   {"https://chatgpt.com/"},
		"GrokAPI":      {"https://cli-chat-proxy.grok.com/", "https://api.x.ai/"},
		"GrokModels":   {"https://cli-chat-proxy.grok.com/", "https://api.x.ai/"},
		"GrokBilling":  {"https://cli-chat-proxy.grok.com/"},
	}
	values := map[string]string{
		"ClaudeAPI": c.Providers.ClaudeAPI, "ClaudeModels": c.Providers.ClaudeModels, "ClaudeUsage": c.Providers.ClaudeUsage,
		"CodexAPI": c.Providers.CodexAPI, "CodexModels": c.Providers.CodexModels, "CodexUsage": c.Providers.CodexUsage,
		"GrokAPI": c.Providers.GrokAPI, "GrokModels": c.Providers.GrokModels, "GrokBilling": c.Providers.GrokBilling,
	}
	for name, value := range values {
		ok := false
		for _, prefix := range allowed[name] {
			if strings.HasPrefix(value, prefix) {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("%s is not an allowed official upstream URL", name)
		}
	}
	return nil
}

func (c Config) validateGrokOAuth() error {
	if strings.TrimSpace(c.GrokOAuth.ClientID) == "" {
		return errors.New("UNISUB_GROK_OAUTH_CLIENT_ID must not be empty")
	}
	if len(c.GrokOAuth.Scopes) == 0 {
		return errors.New("UNISUB_GROK_OAUTH_SCOPES must not be empty")
	}
	if !c.AllowTestUpstreams && strings.TrimRight(c.GrokOAuth.Issuer, "/") != DefaultGrokOAuthIssuer {
		return errors.New("UNISUB_GROK_OAUTH_ISSUER must use the official https://auth.x.ai issuer")
	}
	return nil
}

func (c Config) validateClaudeOAuth() error {
	if strings.TrimSpace(c.ClaudeOAuth.ClientID) == "" {
		return errors.New("UNISUB_CLAUDE_OAUTH_CLIENT_ID must not be empty")
	}
	if len(c.ClaudeOAuth.Scopes) == 0 {
		return errors.New("UNISUB_CLAUDE_OAUTH_SCOPES must not be empty")
	}
	if c.AllowTestUpstreams {
		return nil
	}
	if c.ClaudeOAuth.AuthorizeURL != DefaultClaudeOAuthAuthorizeURL {
		return errors.New("UNISUB_CLAUDE_OAUTH_AUTHORIZE_URL must use the official Claude Code authorize URL")
	}
	if c.ClaudeOAuth.TokenURL != DefaultClaudeOAuthTokenURL {
		return errors.New("UNISUB_CLAUDE_OAUTH_TOKEN_URL must use the official Claude Code token URL")
	}
	if c.ClaudeOAuth.RedirectURI != DefaultClaudeOAuthRedirectURI {
		return errors.New("UNISUB_CLAUDE_OAUTH_REDIRECT_URI must use the official Claude Code callback")
	}
	return nil
}

func (c Config) validateCodexOAuth() error {
	if strings.TrimSpace(c.CodexOAuth.ClientID) == "" {
		return errors.New("UNISUB_CODEX_OAUTH_CLIENT_ID must not be empty")
	}
	if len(c.CodexOAuth.Scopes) == 0 {
		return errors.New("UNISUB_CODEX_OAUTH_SCOPES must not be empty")
	}
	if c.AllowTestUpstreams {
		return nil
	}
	if c.CodexOAuth.AuthorizeURL != DefaultCodexOAuthAuthorizeURL {
		return errors.New("UNISUB_CODEX_OAUTH_AUTHORIZE_URL must use the official Codex authorize URL")
	}
	if c.CodexOAuth.TokenURL != DefaultCodexOAuthTokenURL {
		return errors.New("UNISUB_CODEX_OAUTH_TOKEN_URL must use the official Codex token URL")
	}
	if c.CodexOAuth.RedirectURI != DefaultCodexOAuthRedirectURI {
		return errors.New("UNISUB_CODEX_OAUTH_REDIRECT_URI must use the official Codex localhost callback")
	}
	return nil
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func listenAddress() string {
	if value := strings.TrimSpace(os.Getenv("UNISUB_LISTEN")); value != "" {
		return value
	}
	return ":" + env("UNISUB_PORT", "8080")
}

func durationEnv(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func int64Env(name string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func boolEnv(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func fieldsEnv(name string, fallback []string) []string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return append([]string(nil), fallback...)
	}
	return strings.Fields(strings.ReplaceAll(value, ",", " "))
}
