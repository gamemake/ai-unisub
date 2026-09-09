package server

import (
	"net/http"
	"net/url"
	"strings"
)

func grokCLIUserAgent(version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		version = "0.2.114"
	}
	return "xai-grok-workspace/" + version
}

func applyGrokCLIIdentityHeaders(header http.Header, upstreamURL string) {
	header.Set("X-Grok-Client-Mode", "interactive")
	if parsed, err := url.Parse(upstreamURL); err == nil && strings.EqualFold(parsed.Hostname(), "cli-chat-proxy.grok.com") {
		header.Set("X-XAI-Token-Auth", "xai-grok-cli")
	}
}
