package server

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// Claude Code OAuth credentials are scoped to Claude Code traffic. Anthropic
// classifies requests using User-Agent + anthropic-beta (+ related CLI headers).
//
// Strategy (see docs/sub2api-logic-comparison.md A2/A4/A5):
//   - Real Claude Code (UA matches claude-cli/X.Y.Z): preserve client UA / beta.
//   - Non–Claude Code clients on OAuth: mimic the pinned CLI identity below.
// The pin is not an Anthropic protocol requirement; keep UA and beta in sync when bumping.
const (
	claudeCLIVersionPin       = "2.1.220"
	defaultClaudeCLIUserAgent = "claude-cli/" + claudeCLIVersionPin + " (external, cli)"

	claudeBetaOAuth               = "oauth-2025-04-20"
	claudeBetaClaudeCode          = "claude-code-20250219"
	claudeBetaInterleavedThinking = "interleaved-thinking-2025-05-14"
	claudeBetaPromptCachingScope  = "prompt-caching-scope-2026-01-05"
	claudeBetaEffort              = "effort-2025-11-24"
	claudeBetaContextManagement   = "context-management-2025-06-27"
	claudeBetaExtendedCacheTTL    = "extended-cache-ttl-2025-04-11"
	claudeBetaTokenCounting       = "token-counting-2024-11-01"
)

var claudeCodeUAPattern = regexp.MustCompile(`(?i)^claude-cli/\d+\.\d+\.\d+`)

func isClaudeCodeUserAgent(userAgent string) bool {
	return claudeCodeUAPattern.MatchString(strings.TrimSpace(userAgent))
}

func claudeOAuthMimicryBetas(kind routeKind) string {
	betas := []string{
		claudeBetaClaudeCode,
		claudeBetaOAuth,
		claudeBetaInterleavedThinking,
		claudeBetaPromptCachingScope,
		claudeBetaEffort,
		claudeBetaContextManagement,
		claudeBetaExtendedCacheTTL,
	}
	if kind == routeCountTokens {
		betas = append(betas, claudeBetaTokenCounting)
	}
	return strings.Join(betas, ",")
}

func claudeAPIKeyDefaultBetas(kind routeKind) string {
	betas := []string{
		claudeBetaClaudeCode,
		claudeBetaInterleavedThinking,
	}
	if kind == routeCountTokens {
		betas = append(betas, claudeBetaTokenCounting)
	}
	return strings.Join(betas, ",")
}

func applyClaudeOutboundHeaders(header http.Header, authType string, kind routeKind) {
	if header.Get("anthropic-version") == "" {
		header.Set("anthropic-version", "2023-06-01")
	}
	header.Set("Accept", "application/json")
	if header.Get("x-app") == "" && header.Get("X-App") == "" {
		header.Set("x-app", "cli")
	}

	clientUA := header.Get("User-Agent")
	realClaudeCode := isClaudeCodeUserAgent(clientUA)

	if realClaudeCode {
		// Preserve Claude Code UA and any client betas / stainless headers.
		if header.Get("anthropic-beta") == "" {
			if authType == "api_key" {
				header.Set("anthropic-beta", claudeAPIKeyDefaultBetas(kind))
			} else {
				header.Set("anthropic-beta", claudeOAuthMimicryBetas(kind))
			}
		}
		return
	}

	// Non–Claude Code client: mimic pinned CLI identity for OAuth (and fill gaps for API keys).
	header.Set("User-Agent", defaultClaudeCLIUserAgent)
	if authType == "api_key" {
		if header.Get("anthropic-beta") == "" {
			header.Set("anthropic-beta", claudeAPIKeyDefaultBetas(kind))
		}
	} else {
		header.Set("anthropic-beta", claudeOAuthMimicryBetas(kind))
		header.Set("X-Stainless-Lang", "js")
		header.Set("X-Stainless-Package-Version", "0.94.0")
		header.Set("X-Stainless-OS", "Linux")
		header.Set("X-Stainless-Arch", "arm64")
		header.Set("X-Stainless-Runtime", "node")
		header.Set("X-Stainless-Runtime-Version", "v24.3.0")
		header.Set("X-Stainless-Retry-Count", "0")
		header.Set("X-Stainless-Timeout", "600")
		header.Set("Anthropic-Dangerous-Direct-Browser-Access", "true")
	}
}

func ensureQueryParam(rawURL, key, value string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" {
		return rawURL
	}
	query := parsed.Query()
	if strings.TrimSpace(query.Get(key)) == "" {
		query.Set(key, value)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func appendRawQuery(rawURL, rawQuery string) string {
	rawQuery = strings.TrimSpace(rawQuery)
	if rawQuery == "" {
		return rawURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" {
		if strings.Contains(rawURL, "?") {
			return rawURL + "&" + rawQuery
		}
		return rawURL + "?" + rawQuery
	}
	incoming, err := url.ParseQuery(rawQuery)
	if err != nil {
		return rawURL
	}
	query := parsed.Query()
	for key, values := range incoming {
		for _, value := range values {
			query.Add(key, value)
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
