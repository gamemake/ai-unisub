package server

import (
	"net/http"
	"strings"

	"github.com/ai-unisub/ai-unisub/internal/model"
)

func injectProviderHeaders(header http.Header, provider model.Provider, credentials model.Credentials, upstreamURL string, kind routeKind) {
	header.Set("Content-Type", "application/json")
	switch provider {
	case model.ProviderClaude:
		header.Set("Authorization", "Bearer "+credentials.Bearer())
		applyClaudeOutboundHeaders(header)
	case model.ProviderCodex:
		header.Set("Authorization", "Bearer "+credentials.Bearer())
		applyCodexIdentityHeaders(header)
		header.Set("chatgpt-account-id", credentials.ChatGPTAccountID)
	case model.ProviderGrok:
		header.Set("Authorization", "Bearer "+credentials.Bearer())
		applyGrokCLIIdentityHeaders(header, upstreamURL)
	}
}

// downstreamAPIKey resolves the gateway API key from Anthropic/OpenAI-compatible client headers.
// Preference: Authorization Bearer, then x-api-key, then x-goog-api-key.
func downstreamAPIKey(header http.Header) string {
	if key := bearer(header.Get("Authorization")); key != "" {
		return key
	}
	if key := strings.TrimSpace(header.Get("x-api-key")); key != "" {
		return key
	}
	return strings.TrimSpace(header.Get("x-goog-api-key"))
}

var strippedRequestHeaders = map[string]bool{
	"authorization": true, "proxy-authorization": true, "x-api-key": true, "x-goog-api-key": true, "cookie": true,
	"chatgpt-account-id": true, "host": true, "content-length": true, "connection": true,
	"proxy-connection": true, "keep-alive": true, "transfer-encoding": true, "upgrade": true,
	// Outbound compression is disabled; do not advertise Accept-Encoding or local usage parsing sees gzip bytes.
	"accept-encoding":  true,
	"x-xai-token-auth": true, "x-authenticateresponse": true,
	"x-grok-client-mode": true,
}

func copyDownstreamHeaders(target, source http.Header) {
	for key, values := range source {
		if strippedRequestHeaders[strings.ToLower(key)] {
			continue
		}
		for _, value := range values {
			target.Add(key, value)
		}
	}
}

func copyUpstreamHeaders(target, source http.Header) {
	for key, values := range source {
		switch strings.ToLower(key) {
		case "connection", "keep-alive", "proxy-connection", "transfer-encoding", "upgrade":
			continue
		}
		for _, value := range values {
			target.Add(key, value)
		}
	}
}
