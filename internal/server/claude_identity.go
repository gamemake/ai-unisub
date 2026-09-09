package server

import "net/http"

// Claude Code requests are assumed to come from the official client. Preserve
// its identity headers instead of trying to classify or mimic the client.
const (
	claudeCLIVersionPin       = "2.1.220"
	defaultClaudeCLIUserAgent = "claude-cli/" + claudeCLIVersionPin + " (external, cli)"

	claudeBetaOAuth = "oauth-2025-04-20"
)

func applyClaudeOutboundHeaders(header http.Header) {
	if header.Get("anthropic-version") == "" {
		header.Set("anthropic-version", "2023-06-01")
	}
	if header.Get("x-app") == "" && header.Get("X-App") == "" {
		header.Set("x-app", "cli")
	}
}
