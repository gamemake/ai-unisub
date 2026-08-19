package config

import (
	"crypto/sha256"
	"encoding/base64"
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
	CodexAPI     string
	CodexModels  string
	GrokAPI      string
	GrokModels   string
}

type Config struct {
	ListenAddress       string
	DatabasePath        string
	MasterKey           []byte
	CredentialKeyID     string
	AdminUsername       string
	AdminPassword       string
	AdminTokenTTL       time.Duration
	MaxRequestBodyBytes int64
	ShutdownTimeout     time.Duration
	AllowTestUpstreams  bool
	Providers           ProviderURLs
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddress:       listenAddress(),
		DatabasePath:        env("UNISUB_DB_PATH", "./data/unisub.db"),
		CredentialKeyID:     env("UNISUB_CREDENTIAL_KEY_ID", "local-v1"),
		AdminUsername:       env("UNISUB_ADMIN_USERNAME", "admin"),
		AdminPassword:       env("UNISUB_ADMIN_PASSWORD", "admin"),
		AdminTokenTTL:       durationEnv("UNISUB_ADMIN_TOKEN_TTL", 8*time.Hour),
		MaxRequestBodyBytes: int64Env("UNISUB_MAX_BODY_BYTES", 256<<20),
		ShutdownTimeout:     durationEnv("UNISUB_SHUTDOWN_TIMEOUT", 15*time.Second),
		AllowTestUpstreams:  boolEnv("UNISUB_ALLOW_TEST_UPSTREAMS", false),
		Providers: ProviderURLs{
			ClaudeAPI:    env("UNISUB_CLAUDE_API_URL", "https://api.anthropic.com/v1"),
			ClaudeModels: env("UNISUB_CLAUDE_MODELS_URL", "https://api.anthropic.com/v1/models"),
			CodexAPI:     env("UNISUB_CODEX_API_URL", "https://chatgpt.com/backend-api/codex/responses"),
			CodexModels:  env("UNISUB_CODEX_MODELS_URL", "https://chatgpt.com/backend-api/codex/models"),
			GrokAPI:      env("UNISUB_GROK_API_URL", "https://cli-chat-proxy.grok.com/v1/responses"),
			GrokModels:   env("UNISUB_GROK_MODELS_URL", "https://cli-chat-proxy.grok.com/v1/models"),
		},
	}

	keyText := strings.TrimSpace(os.Getenv("UNISUB_MASTER_KEY"))
	if keyText == "" {
		return Config{}, errors.New("UNISUB_MASTER_KEY is required (base64-encoded 32-byte key)")
	}
	key, err := base64.StdEncoding.DecodeString(keyText)
	if err != nil || len(key) != 32 {
		return Config{}, errors.New("UNISUB_MASTER_KEY must be base64-encoded and decode to exactly 32 bytes")
	}
	cfg.MasterKey = key

	if cfg.MaxRequestBodyBytes < 1024 {
		return Config{}, errors.New("UNISUB_MAX_BODY_BYTES must be at least 1024")
	}
	if err := cfg.validateUpstreams(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) AdminSigningKey() []byte {
	sum := sha256.Sum256(append(append([]byte{}, c.MasterKey...), []byte("admin-session-v1")...))
	return sum[:]
}

func (c Config) validateUpstreams() error {
	if c.AllowTestUpstreams {
		return nil
	}
	allowed := map[string][]string{
		"ClaudeAPI":    {"https://api.anthropic.com/"},
		"ClaudeModels": {"https://api.anthropic.com/"},
		"CodexAPI":     {"https://chatgpt.com/"},
		"CodexModels":  {"https://chatgpt.com/"},
		"GrokAPI":      {"https://cli-chat-proxy.grok.com/", "https://api.x.ai/"},
		"GrokModels":   {"https://cli-chat-proxy.grok.com/", "https://api.x.ai/"},
	}
	values := map[string]string{
		"ClaudeAPI": c.Providers.ClaudeAPI, "ClaudeModels": c.Providers.ClaudeModels,
		"CodexAPI": c.Providers.CodexAPI, "CodexModels": c.Providers.CodexModels,
		"GrokAPI": c.Providers.GrokAPI, "GrokModels": c.Providers.GrokModels,
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
