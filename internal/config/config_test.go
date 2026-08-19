package config

import "testing"

const testMasterKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

func TestLoadUsesDefaultAdminCredentials(t *testing.T) {
	t.Setenv("UNISUB_MASTER_KEY", testMasterKey)
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
}

func TestLoadUsesAdminCredentialsFromEnvironment(t *testing.T) {
	t.Setenv("UNISUB_MASTER_KEY", testMasterKey)
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
	t.Setenv("UNISUB_MASTER_KEY", testMasterKey)
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
	t.Setenv("UNISUB_MASTER_KEY", testMasterKey)
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
