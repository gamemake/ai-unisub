package provider

import (
	"encoding/json"
	"testing"
)

func TestDecodeProviderConfigDefaultsAndProxy(t *testing.T) {
	config, err := decodeProviderConfig("p1", json.RawMessage(`{"name":"n"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !config.Enabled || config.MaxConcurrentConnections != 1 || config.ID != "p1" || config.AuthType != AuthTypeOAuth {
		t.Fatalf("defaults: %+v", config)
	}

	disabled, err := decodeProviderConfig("p1", json.RawMessage(`{"enabled":false,"proxy":"socks5://127.0.0.1:1080","max_concurrent_connections":4,"queue_timeout_seconds":15}`))
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Enabled || disabled.Proxy != "socks5://127.0.0.1:1080" || disabled.MaxConcurrentConnections != 4 || disabled.QueueTimeoutSeconds != 15 {
		t.Fatalf("explicit config: %+v", disabled)
	}

	if _, err := decodeProviderConfig("p1", json.RawMessage(`{"proxy":"ftp://127.0.0.1:21"}`)); err == nil {
		t.Fatal("expected invalid proxy")
	}
}

func TestDecodeProviderConfigAPIKeys(t *testing.T) {
	config, err := decodeProviderConfig("p1", json.RawMessage(`{"auth_type":"api_key","api_endpoint":"https://api.default.test/v1","proxy":"http://default-proxy:8080","api_keys":[{"api_key":" key-one "},{"api_key":"key-two","api_endpoint":"https://api.key.test","proxy":"socks5://key-proxy:1080"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if config.AuthType != AuthTypeAPIKey || len(config.APIKeys) != 2 || config.APIKeys[0].APIKey != "key-one" {
		t.Fatalf("api key config: %+v", config)
	}
	if resolved := config.APIKeys[0].Resolved(config.APIEndpoint, config.Proxy); resolved.APIEndpoint != config.APIEndpoint || resolved.Proxy != config.Proxy {
		t.Fatalf("default API key settings: %+v", resolved)
	}
	if resolved := config.APIKeys[1].Resolved(config.APIEndpoint, config.Proxy); resolved.APIEndpoint != "https://api.key.test" || resolved.Proxy != "socks5://key-proxy:1080" {
		t.Fatalf("per-key API key settings: %+v", resolved)
	}
	if _, err := decodeProviderConfig("p1", json.RawMessage(`{"auth_type":"api_key"}`)); err == nil {
		t.Fatal("expected api keys to be required")
	}
	if _, err := decodeProviderConfig("p1", json.RawMessage(`{"auth_type":"basic","api_keys":[{"api_key":"key"}]}`)); err == nil {
		t.Fatal("expected invalid auth type")
	}
	if _, err := decodeProviderConfig("p1", json.RawMessage(`{"auth_type":"api_key","api_keys":["key"]}`)); err == nil {
		t.Fatal("expected object API key entries")
	}
}
