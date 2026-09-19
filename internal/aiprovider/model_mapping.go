package aiprovider

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

var errModelMappingInvalid = errors.New("model mapping rules must use non-empty trimmed from/to; from allows at most one '*'")

// ModelMapping maps a client model name pattern to an upstream model name.
// From supports a single '*' wildcard (e.g. "claude-*", "gpt-4*").
// To is a literal replacement; if it contains one '*', that star is replaced
// by the substring matched by From's '*'.
type ModelMapping struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// MapModel applies the first matching rule. Unmatched names pass through unchanged.
func MapModel(rules []ModelMapping, model string) string {
	model = strings.TrimSpace(model)
	if model == "" || len(rules) == 0 {
		return model
	}
	for _, rule := range rules {
		if mapped, ok := applyModelRule(rule, model); ok {
			return mapped
		}
	}
	return model
}

func applyModelRule(rule ModelMapping, model string) (string, bool) {
	from := strings.TrimSpace(rule.From)
	to := strings.TrimSpace(rule.To)
	if from == "" || to == "" {
		return "", false
	}
	if !strings.Contains(from, "*") {
		if model == from {
			return to, true
		}
		return "", false
	}
	// Only one '*' is supported in the pattern.
	if strings.Count(from, "*") != 1 {
		return "", false
	}
	prefix, suffix, _ := strings.Cut(from, "*")
	if !strings.HasPrefix(model, prefix) || !strings.HasSuffix(model, suffix) {
		return "", false
	}
	if len(model) < len(prefix)+len(suffix) {
		return "", false
	}
	captured := model[len(prefix) : len(model)-len(suffix)]
	if strings.Count(to, "*") == 1 {
		before, after, _ := strings.Cut(to, "*")
		return before + captured + after, true
	}
	return to, true
}

func sameModelMappings(a, b []ModelMapping) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].From != b[i].From || a[i].To != b[i].To {
			return false
		}
	}
	return true
}

func cloneModelMappings(in []ModelMapping) []ModelMapping {
	if in == nil {
		return nil
	}
	return append([]ModelMapping(nil), in...)
}

func validateModelMappings(rules []ModelMapping) error {
	for _, rule := range rules {
		from := strings.TrimSpace(rule.From)
		to := strings.TrimSpace(rule.To)
		if from == "" || to == "" {
			return errModelMappingInvalid
		}
		if from != rule.From || to != rule.To {
			return errModelMappingInvalid
		}
		if strings.Count(from, "*") > 1 {
			return errModelMappingInvalid
		}
	}
	return nil
}

// RewriteRequestModel applies supplier model mappings to the top-level JSON
// "model" field and the Grok override header. Nested fields (e.g. metadata.model)
// are left unchanged. Returns the possibly rewritten body.
func RewriteRequestModel(body []byte, headers http.Header, rules []ModelMapping) []byte {
	if len(rules) == 0 {
		return body
	}
	if headers != nil {
		if override := headers.Get("X-Grok-Model-Override"); override != "" {
			if mapped := MapModel(rules, override); mapped != override {
				headers.Set("X-Grok-Model-Override", mapped)
			}
		}
	}
	if len(body) == 0 || !bytes.Contains(body, []byte(`"model"`)) {
		return body
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(body, &payload) != nil {
		return body
	}
	raw, ok := payload["model"]
	if !ok {
		return body
	}
	var model string
	if json.Unmarshal(raw, &model) != nil || model == "" {
		return body
	}
	mapped := MapModel(rules, model)
	if mapped == model {
		return body
	}
	encoded, err := json.Marshal(mapped)
	if err != nil {
		return body
	}
	payload["model"] = encoded
	out, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return out
}
