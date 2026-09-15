package aiprovider

import (
	"encoding/json"
	"testing"
)

func TestDecodeAIProviderConfigDefaultsAndProxy(t *testing.T) {
	config, err := decodeAIProviderConfig("p1", json.RawMessage(`{"name":"n"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !config.Enabled || config.MaxConcurrentConnections != 1 || config.ID != "p1" || config.AuthType != AuthTypeOAuth {
		t.Fatalf("defaults: %+v", config)
	}

	disabled, err := decodeAIProviderConfig("p1", json.RawMessage(`{"enabled":false,"proxy_group_id":"group-1","max_concurrent_connections":4,"queue_timeout_seconds":15}`))
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Enabled || disabled.ProxyGroupID != "group-1" || disabled.MaxConcurrentConnections != 4 || disabled.QueueTimeoutSeconds != 15 {
		t.Fatalf("explicit config: %+v", disabled)
	}

	for _, raw := range []string{`{"proxy":"ftp://127.0.0.1:21"}`, `{"proxy":null}`, `{"proxy":{"ignored":true}}`} {
		got, err := decodeAIProviderConfig("p1", json.RawMessage(raw))
		if err != nil || got.ProxyGroupID != "" {
			t.Fatalf("unknown proxy field must be ignored: %+v, %v", got, err)
		}
	}
	got, err := decodeAIProviderConfig("p1", json.RawMessage(`{"proxy":"invalid","proxy_group_id":"group-1"}`))
	if err != nil || got.ProxyGroupID != "group-1" {
		t.Fatalf("proxy must not affect proxy_group_id: %+v, %v", got, err)
	}
}

func TestDecodeAIProviderConfigAPIKey(t *testing.T) {
	config, err := decodeAIProviderConfig("p1", json.RawMessage(`{"auth_type":"api_key","api_endpoint":"https://api.default.test/v1","api_key":" key-one "}`))
	if err != nil {
		t.Fatal(err)
	}
	if config.AuthType != AuthTypeAPIKey || config.APIKey != "key-one" || config.APIEndpoint != "https://api.default.test/v1" {
		t.Fatalf("api key config: %+v", config)
	}
	if _, err := decodeAIProviderConfig("p1", json.RawMessage(`{"auth_type":"api_key"}`)); err == nil {
		t.Fatal("expected api key to be required")
	}
	if _, err := decodeAIProviderConfig("p1", json.RawMessage(`{"auth_type":"basic","api_key":"key"}`)); err == nil {
		t.Fatal("expected invalid auth type")
	}
	if _, err := decodeAIProviderConfig("p1", json.RawMessage(`{"auth_type":"api_key","api_key":"key","api_keys":[{"api_key":"old"}]}`)); err == nil {
		t.Fatal("expected legacy api_keys to be rejected")
	}
	if _, err := decodeAIProviderConfig("p1", json.RawMessage(`{"auth_type":"oauth","api_key":"key"}`)); err == nil {
		t.Fatal("expected oauth configs to reject api_key")
	}
}
