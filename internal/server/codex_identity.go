package server

import "net/http"

// Codex outbound identity aligned with ChatGPT backend-api/codex expectations.
// Upstream 404s version headers below 0.144.0 (see sub2api issue #3901).
const (
	codexOriginator         = "codex-tui"
	codexClientVersion      = "0.146.0"
	codexCLIUserAgentSuffix = " (Ubuntu 22.4.0; x86_64) xterm-256color"
)

func codexCLIUserAgent() string {
	return codexOriginator + "/" + codexClientVersion + codexCLIUserAgentSuffix
}

func applyCodexIdentityHeaders(header http.Header) {
	header.Set("User-Agent", codexCLIUserAgent())
	header.Set("originator", codexOriginator)
	header.Set("version", codexClientVersion)
}
