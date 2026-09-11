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
	if !config.Enabled || config.MaxConcurrentConnections != 1 || config.ID != "p1" {
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
