package aiprovider

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
)

type ClientType string

const (
	ClientAnthropic ClientType = "Anthropic"
	ClientOpenAI    ClientType = "OpenAI"
	ClientGrok      ClientType = "Grok"
	ClientAny       ClientType = "Any"
)

var ErrClientDenied = errors.New("client type is not allowed")
var clientPatterns = []struct {
	kind    ClientType
	pattern *regexp.Regexp
}{
	{ClientAnthropic, regexp.MustCompile(`(?i)(?:^|[\s(])claude-cli/[0-9]`)},
	{ClientOpenAI, regexp.MustCompile(`(?i)(?:^|[\s(])codex(?:_cli_rs|_cli|[- ]cli|-tui)?/[0-9]`)},
	{ClientGrok, regexp.MustCompile(`(?i)(?:^|[\s(])(?:grok(?:-cli|-shell)?|xai-grok-workspace)/[0-9]`)},
}

// Unknown and ambiguous clients remain empty, not Any (Any is a policy).
func DetectClient(h http.Header) ClientType {
	if len(h.Values("User-Agent")) != 1 {
		return ""
	}
	var found ClientType
	for _, p := range clientPatterns {
		if p.pattern.MatchString(h.Get("User-Agent")) {
			if found != "" {
				return ""
			}
			found = p.kind
		}
	}
	return found
}
func SupplierForAdapter(adapter string) string {
	switch adapter {
	case "claude":
		return "anthropic"
	case "codex":
		return "openai"
	case "grok":
		return "grok"
	}
	return ""
}
func effectiveClient(c AIProviderConfig) ClientType {
	if c.OfficialOnly {
		switch c.Supplier {
		case "anthropic":
			return ClientAnthropic
		case "openai":
			return ClientOpenAI
		case "grok":
			return ClientGrok
		}
		return ""
	}
	if c.ClientType == "" {
		return ClientAny
	}
	return c.ClientType
}
func AllowsClient(c AIProviderConfig, client ClientType) bool {
	allowed := effectiveClient(c)
	return allowed == ClientAny || allowed != "" && allowed == client
}
func validateRoutingConfig(c *AIProviderConfig) error {
	if c.Kind == "" {
		if c.AuthType == AuthTypeAPIKey {
			c.Kind = "api"
		} else {
			c.Kind = "subscription"
		}
	}
	if c.Kind != "api" && c.Kind != "subscription" && c.Kind != "group" {
		return errors.New("invalid provider kind")
	}
	if c.Kind == "api" && c.AuthType != AuthTypeAPIKey || c.Kind == "subscription" && c.AuthType != AuthTypeOAuth {
		return errors.New("provider kind and authentication disagree")
	}
	if c.OfficialOnly && c.Kind != "subscription" {
		return errors.New("official-only requires a subscription")
	}
	if c.ClientType == "" {
		c.ClientType = ClientAny
	}
	switch c.ClientType {
	case ClientAny, ClientAnthropic, ClientOpenAI, ClientGrok:
	default:
		return errors.New("invalid client type")
	}
	if c.Kind != "group" && len(c.Members) > 0 {
		return errors.New("only groups may contain members")
	}
	return nil
}

// Native session headers are client-scoped. Conversation and request IDs are
// not interchangeable: only Grok's conversation ID is an explicit fallback.
func SessionID(h http.Header) (string, error) {
	var names []string
	switch DetectClient(h) {
	case ClientAnthropic:
		names = append(names, "X-Claude-Code-Session-Id")
	case ClientOpenAI:
		names = append(names, "Session-Id", "Session_id", "X-Session-Id")
	case ClientGrok:
		names = append(names, "X-Grok-Session-Id", "X-Grok-Conv-Id")
	}
	for _, name := range names {
		values := h.Values(name)
		if len(values) == 0 {
			continue
		}
		if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > 128 {
			return "", errors.New("invalid session ID")
		}
		for _, c := range values[0] {
			if c < 33 || c == 127 || c == ',' {
				return "", errors.New("invalid session ID")
			}
		}
		return strings.TrimSpace(values[0]), nil
	}
	return "", nil
}
