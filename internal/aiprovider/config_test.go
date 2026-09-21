package aiprovider

import (
	"encoding/json"
	"testing"
)

func TestDecodeAIProviderConfigDefaultsAndProxy(t *testing.T) {
	config, err := decodeAIProviderConfig(1, json.RawMessage(`{"name":"n"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !config.Enabled || config.MaxConcurrentConnections != 1 || config.ID != 1 || config.AuthType != AuthTypeOAuth {
		t.Fatalf("defaults: %+v", config)
	}

	disabled, err := decodeAIProviderConfig(1, json.RawMessage(`{"enabled":false,"proxy_group_id":1,"max_concurrent_connections":4,"queue_timeout_seconds":15}`))
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Enabled || disabled.ProxyGroupID != 1 || disabled.MaxConcurrentConnections != 4 || disabled.QueueTimeoutSeconds != 15 {
		t.Fatalf("explicit config: %+v", disabled)
	}

	for _, raw := range []string{`{"proxy":"ftp://127.0.0.1:21"}`, `{"proxy":null}`, `{"proxy":{"ignored":true}}`} {
		got, err := decodeAIProviderConfig(1, json.RawMessage(raw))
		if err != nil || got.ProxyGroupID != 0 {
			t.Fatalf("unknown proxy field must be ignored: %+v, %v", got, err)
		}
	}
	got, err := decodeAIProviderConfig(1, json.RawMessage(`{"proxy":"invalid","proxy_group_id":1}`))
	if err != nil || got.ProxyGroupID != 1 {
		t.Fatalf("proxy must not affect proxy_group_id: %+v, %v", got, err)
	}
}

func TestPrepareConfigSubscriptionPlan(t *testing.T) {
	raw, err := prepareConfig(1, "codex", json.RawMessage(`{"kind":"subscription","credential_id":"c"}`))
	if err != nil {
		t.Fatal(err)
	}
	c, err := decodeAIProviderConfig(1, raw)
	if err != nil {
		t.Fatal(err)
	}
	if c.SubscriptionPlan != PlanCodexPlus {
		t.Fatalf("codex default plan: %q", c.SubscriptionPlan)
	}

	raw, err = prepareConfig(1, "claude", json.RawMessage(`{"kind":"subscription","credential_id":"c","subscription_plan":"claude_max_20x"}`))
	if err != nil {
		t.Fatal(err)
	}
	c, err = decodeAIProviderConfig(1, raw)
	if err != nil || c.SubscriptionPlan != PlanClaudeMax20x {
		t.Fatalf("claude plan: %+v %v", c, err)
	}

	raw, err = prepareConfig(1, "dummy", json.RawMessage(`{"kind":"subscription"}`))
	if err != nil {
		t.Fatal(err)
	}
	c, err = decodeAIProviderConfig(1, raw)
	if err != nil || c.SubscriptionPlan != PlanClaudePro {
		t.Fatalf("dummy uses claude plans: %+v %v", c, err)
	}

	raw, err = prepareConfig(1, "grok", json.RawMessage(`{"kind":"subscription","credential_id":"c","subscription_plan":"super_grok_heavy"}`))
	if err != nil {
		t.Fatal(err)
	}
	c, err = decodeAIProviderConfig(1, raw)
	if err != nil || c.SubscriptionPlan != PlanSuperGrokHeavy {
		t.Fatalf("grok plan: %+v %v", c, err)
	}

	if _, err := prepareConfig(1, "codex", json.RawMessage(`{"kind":"subscription","credential_id":"c","subscription_plan":"plus"}`)); err == nil {
		t.Fatal("unprefixed plan must be rejected")
	}
	for _, plan := range []string{"claude_free", "claude_max"} {
		if _, err := prepareConfig(1, "claude", json.RawMessage(`{"kind":"subscription","credential_id":"c","subscription_plan":"`+plan+`"}`)); err == nil {
			t.Fatalf("removed plan %q must be rejected", plan)
		}
	}
	if _, err := prepareConfig(1, "codex", json.RawMessage(`{"kind":"subscription","credential_id":"c","subscription_plan":"codex_pro"}`)); err == nil {
		t.Fatal("codex_pro baseline must be rejected")
	}
	if _, err := prepareConfig(1, "api", json.RawMessage(`{"kind":"api","auth_type":"api_key","api_key":"k","supplier":"openai","subscription_plan":"codex_plus"}`)); err == nil {
		t.Fatal("api must reject subscription_plan")
	}

	raw, err = prepareConfig(1, "api", json.RawMessage(`{"kind":"api","auth_type":"api_key","api_key":"k","supplier":"openai"}`))
	if err != nil {
		t.Fatal(err)
	}
	c, err = decodeAIProviderConfig(1, raw)
	if err != nil || c.SubscriptionPlan != "" {
		t.Fatalf("api plan must stay empty: %+v %v", c, err)
	}
}

func TestDecodeAIProviderConfigAPIKey(t *testing.T) {
	config, err := decodeAIProviderConfig(1, json.RawMessage(`{"auth_type":"api_key","api_endpoint":"https://api.default.test/v1","api_key":" key-one "}`))
	if err != nil {
		t.Fatal(err)
	}
	if config.AuthType != AuthTypeAPIKey || config.APIKey != "key-one" || config.APIEndpoint != "https://api.default.test/v1" {
		t.Fatalf("api key config: %+v", config)
	}
	if _, err := decodeAIProviderConfig(1, json.RawMessage(`{"auth_type":"api_key"}`)); err == nil {
		t.Fatal("expected api key to be required")
	}
	if _, err := decodeAIProviderConfig(1, json.RawMessage(`{"auth_type":"basic","api_key":"key"}`)); err == nil {
		t.Fatal("expected invalid auth type")
	}
	if _, err := decodeAIProviderConfig(1, json.RawMessage(`{"auth_type":"api_key","api_key":"key","api_keys":[{"api_key":"old"}]}`)); err == nil {
		t.Fatal("expected legacy api_keys to be rejected")
	}
	if _, err := decodeAIProviderConfig(1, json.RawMessage(`{"auth_type":"oauth","api_key":"key"}`)); err == nil {
		t.Fatal("expected oauth configs to reject api_key")
	}
}
