package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestSanitizeHeadersJSONRedactsSecretsAndKeepsOthers(t *testing.T) {
	encoded := sanitizeHeadersJSON(http.Header{
		"Authorization": []string{"Bearer secret-token"},
		"X-Api-Key":     []string{"sk-live"},
		"Cookie":        []string{"session=abc"},
		"Content-Type":  []string{"application/json"},
		"X-Custom":      []string{"trace", "span"},
	})
	for _, leaked := range []string{"secret-token", "sk-live", "session=abc"} {
		if strings.Contains(encoded, leaked) {
			t.Fatalf("secret leaked in %s", encoded)
		}
	}
	for _, marker := range []string{`"[redacted]"`, `"Content-Type"`, `"application/json"`, `"X-Custom"`, `"trace"`, `"span"`} {
		if !strings.Contains(encoded, marker) {
			t.Fatalf("missing %s in %s", marker, encoded)
		}
	}
}

func TestSanitizeHeadersJSONEmpty(t *testing.T) {
	if got := sanitizeHeadersJSON(nil); got != "{}" {
		t.Fatalf("nil headers = %q", got)
	}
	if got := sanitizeHeadersJSON(http.Header{}); got != "{}" {
		t.Fatalf("empty headers = %q", got)
	}
}
