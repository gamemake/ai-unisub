package config

import "testing"

import "time"

func TestLoadUsesDefaultAdminCredentials(t *testing.T) {
	t.Setenv("UNISUB_ADMIN_USERNAME", "")
	t.Setenv("UNISUB_ADMIN_PASSWORD", "")

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
