package aiprovider

import (
	"net/http"
	"testing"
)

func TestMapModel(t *testing.T) {
	rules := []ModelMapping{
		{From: "exact", To: "mapped-exact"},
		{From: "claude-*", To: "ds-*"},
		{From: "gpt-*", To: "gpt-4o"},
		{From: "*-preview", To: "stable"},
	}
	cases := []struct{ in, want string }{
		{"exact", "mapped-exact"},
		{"claude-sonnet-4", "ds-sonnet-4"},
		{"gpt-5-mini", "gpt-4o"},
		{"foo-preview", "stable"},
		{"untouched", "untouched"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := MapModel(rules, tc.in); got != tc.want {
			t.Fatalf("%q: got %q want %q", tc.in, got, tc.want)
		}
	}
	if got := MapModel(nil, "x"); got != "x" {
		t.Fatal(got)
	}
}

func TestRewriteRequestModel(t *testing.T) {
	rules := []ModelMapping{{From: "client-*", To: "up-*"}}
	body := []byte(`{"model":"client-a","metadata":{"model":"keep"},"input":"hi"}`)
	headers := http.Header{"X-Grok-Model-Override": {"client-b"}}
	out := RewriteRequestModel(body, headers, rules)
	if string(out) == string(body) {
		t.Fatal("body not rewritten")
	}
	if headers.Get("X-Grok-Model-Override") != "up-b" {
		t.Fatalf("header=%q", headers.Get("X-Grok-Model-Override"))
	}
	if !containsAll(string(out), `"model":"up-a"`, `"model":"keep"`) {
		t.Fatalf("body=%s", out)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !contains(s, p) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestDiffSupplierModelMappings(t *testing.T) {
	b, _ := builtinByID("deepseek")
	overlay, changed := DiffSupplier(b, SupplierConfigurable{Name: b.Name, Models: append([]string(nil), b.Models...), ModelMappings: []ModelMapping{{From: "a*", To: "b"}}})
	if !changed || overlay.ModelMappings == nil || len(*overlay.ModelMappings) != 1 {
		t.Fatalf("overlay=%+v changed=%v", overlay, changed)
	}
	merged := MergeSupplier(b, overlay)
	if len(merged.ModelMappings) != 1 || merged.ModelMappings[0].From != "a*" {
		t.Fatalf("merged=%+v", merged)
	}
	overlay, changed = DiffSupplier(b, SupplierConfigurable{Name: b.Name, Models: append([]string(nil), b.Models...), ModelMappings: nil})
	if changed {
		t.Fatal("empty mappings should match builtin")
	}
}
