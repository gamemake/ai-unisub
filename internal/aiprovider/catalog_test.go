package aiprovider

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClientTypeIsSingleValueAndIgnoresOldField(t *testing.T) {
	for _, raw := range []string{`{}`, `{"client_types":["OpenAI","Grok"]}`, `{"client_types":{"invalid":"ignored"}}`} {
		c, err := decodeAIProviderConfig("a", json.RawMessage(raw))
		if err != nil || c.ClientType != ClientAny {
			t.Fatalf("default/old field: %s %+v %v", raw, c, err)
		}
	}
	for _, raw := range []string{`{"client_type":["OpenAI"]}`, `{"client_type":"Other"}`} {
		if _, err := decodeAIProviderConfig("a", json.RawMessage(raw)); err == nil {
			t.Fatal("accepted non-scalar/invalid client type")
		}
	}
	raw, err := prepareConfig("a", "dummy", json.RawMessage(`{"client_type":"Grok","client_types":["OpenAI"]}`))
	if err != nil || strings.Contains(string(raw), "client_types") {
		t.Fatalf("old array retained: %s %v", raw, err)
	}
	c, err := decodeAIProviderConfig("a", raw)
	if err != nil || !AllowsClient(c, ClientGrok) || AllowsClient(c, ClientOpenAI) || AllowsClient(c, "") {
		t.Fatalf("single policy not applied: %+v %v", c, err)
	}
}
