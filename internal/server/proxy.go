package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
	"github.com/ai-unisub/ai-unisub/internal/repository"
	"github.com/gin-gonic/gin"
)

type routeKind int

const (
	routeModels routeKind = iota
	routeMessages
	routeCountTokens
	routeResponses
)

var safePathSegment = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func (s *Server) proxyHandler(expectedProvider string, kind routeKind, pathParam string) gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		capture := newCapturingWriter(c.Writer, requestLogBodyMaxBytes)
		c.Writer = capture
		requestLog := repository.RequestLog{
			Method: c.Request.Method, Path: c.Request.URL.Path, Query: c.Request.URL.RawQuery, ClientIP: c.ClientIP(), StartedAt: started,
			RequestHeaders: sanitizeHeadersJSON(c.Request.Header),
		}
		var requestBody []byte
		defer func() {
			requestLog.FinishedAt = time.Now()
			requestLog.StatusCode = c.Writer.Status()
			if errorType, ok := c.Get("error_type"); ok {
				requestLog.ErrorType, _ = errorType.(string)
			}
			if c.Request.Context().Err() != nil && requestLog.StatusCode < 400 {
				requestLog.StatusCode = 499
				requestLog.ErrorType = "client_canceled"
			}
			requestLog.RequestBody, requestLog.RequestTruncated = truncateForLog(requestBody, requestLogBodyMaxBytes)
			requestLog.ResponseHeaders = sanitizeHeadersJSON(c.Writer.Header())
			requestLog.ResponseBody = capture.Body()
			requestLog.ResponseTruncated = capture.Truncated()
			tokens := capture.TokenUsage()
			if requestLog.Model == "" {
				requestLog.Model = firstNonEmpty(tokens.Model, requestModel(requestBody))
			}
			requestLog.InputTokens = tokens.Input
			requestLog.OutputTokens = tokens.Output
			requestLog.CacheReadTokens = tokens.CacheRead
			requestLog.CacheCreationTokens = tokens.CacheCreation
			requestLog.TotalTokens = tokens.Total
			if err := s.repo.RecordRequest(time.Local, requestLog); err != nil {
				slog.Error("record request failed", "method", requestLog.Method, "path", requestLog.Path, "error", err)
			}
		}()
		plaintextKey := downstreamAPIKey(c.Request.Header)
		account, err := s.repo.ResolveAPIKey(c.Request.Context(), plaintextKey)
		if err != nil {
			if errors.Is(err, repository.ErrUnauthorized) {
				apiError(c, 401, "invalid_api_key", "a valid account API key is required")
				return
			}
			apiError(c, 500, "internal_error", "could not resolve API key")
			return
		}
		requestLog.SubscriptionID = &account.ID
		requestLog.APIKeyID = &account.APIKeyID
		requestLog.UserID = account.UserID
		requestLog.Provider = string(account.Provider)
		if expectedProvider != "" && string(account.Provider) != expectedProvider {
			apiError(c, 403, "provider_mismatch", "API key is bound to a different provider")
			return
		}
		if kind == routeMessages || kind == routeCountTokens {
			if account.Provider != model.ProviderClaude {
				apiError(c, 403, "provider_mismatch", "Anthropic endpoints require a Claude account key")
				return
			}
		}
		if kind == routeResponses && account.Provider == model.ProviderClaude {
			apiError(c, 403, "provider_mismatch", "Responses endpoints require a Codex or Grok account key")
			return
		}
		credentials, err := s.repo.Credentials(c.Request.Context(), account.Subscription)
		if err != nil || credentials.Bearer() == "" {
			apiError(c, 502, "credential_error", "account credentials are unavailable")
			return
		}
		credentials, err = s.credentialsForRequest(c.Request.Context(), account.Subscription, credentials, false)
		if err != nil {
			apiError(c, 401, "reauth_required", err.Error())
			return
		}
		if account.RPMLimit != nil && !s.allowRate(fmt.Sprintf("api:%d", account.APIKeyID), *account.RPMLimit, time.Minute) {
			c.Header("Retry-After", "60")
			apiError(c, http.StatusTooManyRequests, "rate_limited", "API key requests-per-minute limit exceeded")
			return
		}

		suffix := ""
		if pathParam != "" {
			suffix = c.Param(pathParam)
			if err := validateResponseSubpath(suffix, c.Request.URL.EscapedPath()); err != nil {
				apiError(c, 404, "invalid_subpath", "Responses subpath is not allowed")
				return
			}
		}
		body, err := readBody(c.Request, s.cfg.MaxRequestBodyBytes)
		requestBody = body
		if err != nil {
			if errors.Is(err, errBodyTooLarge) {
				apiError(c, 413, "request_too_large", "request body exceeds configured limit")
			} else {
				apiError(c, 400, "invalid_request", err.Error())
			}
			return
		}
		requestLog.Model = requestModel(body)
		if kind != routeModels && !json.Valid(body) {
			apiError(c, 400, "invalid_json", "request body must be valid JSON")
			return
		}
		if (kind == routeMessages || (kind == routeResponses && suffix == "")) && !hasStringModel(body) {
			apiError(c, 400, "invalid_request", "request body must include a non-empty model")
			return
		}

		upstreamURL, err := s.upstreamURL(account.Provider, kind, suffix)
		if err != nil {
			apiError(c, 501, "unsupported_endpoint", err.Error())
			return
		}
		upstreamURL = appendRawQuery(upstreamURL, c.Request.URL.RawQuery)
		if account.Provider == model.ProviderClaude && (kind == routeMessages || kind == routeCountTokens) {
			upstreamURL = ensureQueryParam(upstreamURL, "beta", "true")
		}

		release, err := s.acquire(c.Request.Context(), account.ID, account.ConcurrencyLimit, s.queueTimeout(account.Subscription))
		if err != nil {
			if errors.Is(err, errConcurrencyQueueTimeout) {
				apiError(c, http.StatusTooManyRequests, "concurrency_limited", "account concurrency queue wait timed out")
			}
			return
		}
		defer release()
		client, err := s.clientForSubscription(account.Subscription)
		if err != nil {
			apiError(c, 502, "proxy_error", err.Error())
			return
		}
		buildUpstreamRequest := func(currentCredentials model.Credentials) (*http.Request, error) {
			request, buildErr := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, upstreamURL, bytes.NewReader(body))
			if buildErr != nil {
				return nil, buildErr
			}
			copyDownstreamHeaders(request.Header, c.Request.Header)
			injectProviderHeaders(request.Header, account.Provider, account.AuthType, currentCredentials, upstreamURL, s.cfg.GrokOAuth.ClientVersion, kind)
			return request, nil
		}
		upstreamRequest, err := buildUpstreamRequest(credentials)
		if err != nil {
			apiError(c, 500, "internal_error", "could not build upstream request")
			return
		}
		response, err := doHTTP(client, upstreamRequest)
		if err != nil {
			apiError(c, 502, "upstream_unavailable", "could not connect to upstream provider")
			return
		}
		if response.StatusCode == http.StatusUnauthorized && account.AuthType == "oauth" && credentials.RefreshToken != "" {
			response.Body.Close()
			credentials, err = s.credentialsForRequest(c.Request.Context(), account.Subscription, credentials, true)
			if err != nil {
				apiError(c, http.StatusUnauthorized, "reauth_required", err.Error())
				return
			}
			upstreamRequest, err = buildUpstreamRequest(credentials)
			if err != nil {
				apiError(c, 500, "internal_error", "could not retry upstream request")
				return
			}
			response, err = doHTTP(client, upstreamRequest)
			if err != nil {
				apiError(c, 502, "upstream_unavailable", "could not connect to upstream provider after refreshing credentials")
				return
			}
		}
		defer response.Body.Close()
		quotaCheckedAt := time.Now()
		if quota, ok := quotaFromResponseHeaders(account.Quota, response.Header, quotaCheckedAt); ok {
			if err := s.repo.UpdateSubscriptionQuota(c.Request.Context(), account.ID, quota, quotaCheckedAt, nil); err != nil {
				slog.Warn("update account quota failed", "subscription_id", account.ID, "error", err)
			}
		}
		contentType := strings.ToLower(response.Header.Get("Content-Type"))
		if strings.Contains(contentType, "text/event-stream") {
			c.Writer.Header().Set("X-Accel-Buffering", "no")
		}
		copyUpstreamHeaders(c.Writer.Header(), response.Header)
		requestID := firstNonEmpty(response.Header.Get("request-id"), response.Header.Get("x-request-id"))
		requestLog.RequestID = requestID
		c.Status(response.StatusCode)
		if strings.Contains(contentType, "text/event-stream") {
			relaySSE(c.Writer, response.Body)
		} else {
			_, _ = io.Copy(c.Writer, response.Body)
		}
	}
}

func (s *Server) queueTimeout(account model.Subscription) time.Duration {
	if account.ConcurrencyQueueTimeoutSeconds > 0 {
		return time.Duration(account.ConcurrencyQueueTimeoutSeconds) * time.Second
	}
	return s.cfg.ConcurrencyQueueTimeout
}

func (s *Server) upstreamURL(provider model.Provider, kind routeKind, suffix string) (string, error) {
	switch provider {
	case model.ProviderClaude:
		switch kind {
		case routeModels:
			return s.cfg.Providers.ClaudeModels, nil
		case routeMessages:
			return strings.TrimRight(s.cfg.Providers.ClaudeAPI, "/") + "/messages", nil
		case routeCountTokens:
			return strings.TrimRight(s.cfg.Providers.ClaudeAPI, "/") + "/messages/count_tokens", nil
		}
	case model.ProviderCodex:
		if kind == routeModels {
			return s.cfg.Providers.CodexModels, nil
		}
		if kind == routeResponses {
			return strings.TrimRight(s.cfg.Providers.CodexAPI, "/") + suffix, nil
		}
	case model.ProviderGrok:
		if kind == routeModels {
			return s.cfg.Providers.GrokModels, nil
		}
		if kind == routeResponses {
			return strings.TrimRight(s.cfg.Providers.GrokAPI, "/") + suffix, nil
		}
	}
	return "", fmt.Errorf("endpoint is not supported by %s", provider)
}

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

func grokCLIUserAgent(version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		version = "0.2.114"
	}
	return "xai-grok-workspace/" + version
}

func applyGrokCLIIdentityHeaders(header http.Header, version, upstreamURL string) {
	version = strings.TrimSpace(version)
	header.Set("User-Agent", grokCLIUserAgent(version))
	header.Set("X-Grok-Client-Version", version)
	header.Set("x-grok-client-version", version)
	header.Set("x-grok-client-identifier", "grok-shell")
	header.Set("X-Grok-Client-Mode", "interactive")
	if parsed, err := url.Parse(upstreamURL); err == nil && strings.EqualFold(parsed.Hostname(), "cli-chat-proxy.grok.com") {
		header.Set("X-XAI-Token-Auth", "xai-grok-cli")
	}
}

func injectProviderHeaders(header http.Header, provider model.Provider, authType string, credentials model.Credentials, upstreamURL, grokClientVersion string, kind routeKind) {
	header.Set("Content-Type", "application/json")
	switch provider {
	case model.ProviderClaude:
		if authType == "api_key" {
			header.Set("x-api-key", credentials.Bearer())
		} else {
			header.Set("Authorization", "Bearer "+credentials.Bearer())
		}
		applyClaudeOutboundHeaders(header, authType, kind)
	case model.ProviderCodex:
		header.Set("Authorization", "Bearer "+credentials.Bearer())
		header.Set("Accept", "text/event-stream")
		applyCodexIdentityHeaders(header)
		header.Set("chatgpt-account-id", credentials.ChatGPTAccountID)
	case model.ProviderGrok:
		header.Set("Authorization", "Bearer "+credentials.Bearer())
		header.Set("Accept", "application/json, text/event-stream")
		applyGrokCLIIdentityHeaders(header, grokClientVersion, upstreamURL)
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
	"x-xai-token-auth": true, "x-authenticateresponse": true, "x-grok-client-version": true,
	"x-grok-client-identifier": true, "x-grok-client-mode": true,
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

var errBodyTooLarge = errors.New("request body too large")

func readBody(request *http.Request, max int64) ([]byte, error) {
	if request.Body == nil {
		return nil, nil
	}
	reader := io.LimitReader(request.Body, max+1)
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, errors.New("could not read request body")
	}
	if int64(len(body)) > max {
		return nil, errBodyTooLarge
	}
	return body, nil
}

func hasStringModel(body []byte) bool {
	var envelope struct {
		Model string `json:"model"`
	}
	return json.Unmarshal(body, &envelope) == nil && strings.TrimSpace(envelope.Model) != ""
}

func validateResponseSubpath(suffix, escapedPath string) error {
	if suffix == "" || !strings.HasPrefix(suffix, "/") || strings.Contains(escapedPath, "%") {
		return errors.New("invalid prefix")
	}
	segments := strings.Split(strings.TrimPrefix(suffix, "/"), "/")
	if len(segments) == 0 || len(segments) > 8 {
		return errors.New("invalid segment count")
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." || len(segment) > 128 || !safePathSegment.MatchString(segment) {
			return errors.New("invalid path segment")
		}
	}
	return nil
}

func relaySSE(writer gin.ResponseWriter, source io.Reader) {
	reader := bufio.NewReader(source)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			_, _ = writer.Write(line)
			writer.Flush()
		}
		if err != nil {
			return
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func validateOfficialURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("invalid upstream URL")
	}
	return nil
}
