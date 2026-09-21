package aiprovider

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClientTypeIsSingleValueAndIgnoresOldField(t *testing.T) {
	for _, raw := range []string{`{}`, `{"client_types":["codex","grok"]}`, `{"client_types":{"invalid":"ignored"}}`} {
		c, err := decodeAIProviderConfig(1, json.RawMessage(raw))
		if err != nil || c.ClientType != "" {
			t.Fatalf("default/old field: %s %+v %v", raw, c, err)
		}
	}
	for _, raw := range []string{`{"client_type":["codex"]}`, `{"client_type":"Other"}`} {
		if _, err := decodeAIProviderConfig(1, json.RawMessage(raw)); err == nil {
			t.Fatal("accepted non-scalar/invalid client type")
		}
	}
	raw, err := prepareConfig(1, "dummy", json.RawMessage(`{"client_type":"grok","client_types":["codex"]}`))
	if err != nil || strings.Contains(string(raw), "client_types") {
		t.Fatalf("old array retained: %s %v", raw, err)
	}
	c, err := decodeAIProviderConfig(1, raw)
	if err != nil || !AllowsClient(c, ClientGrok) || AllowsClient(c, ClientCodex) || AllowsClient(c, "") {
		t.Fatalf("single policy not applied: %+v %v", c, err)
	}
}

func TestLegacyClientTypeValuesNormalize(t *testing.T) {
	for _, tc := range []struct {
		legacy string
		want   ClientType
	}{
		{legacy: "Anthropic", want: ClientClaude},
		{legacy: "OpenAI", want: ClientCodex},
		{legacy: "Grok", want: ClientGrok},
		{legacy: "Any", want: ""},
	} {
		t.Run(tc.legacy, func(t *testing.T) {
			c, err := decodeAIProviderConfig(1, json.RawMessage(`{"client_type":"`+tc.legacy+`"}`))
			if err != nil || c.ClientType != tc.want {
				t.Fatalf("got %q %v, want %q", c.ClientType, err, tc.want)
			}
		})
	}
}
