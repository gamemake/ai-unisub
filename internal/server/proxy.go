package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
		started := time.Now().UTC()
		plaintextKey := bearer(c.GetHeader("Authorization"))
		account, err := s.repo.ResolveAPIKey(c.Request.Context(), plaintextKey)
		if err != nil {
			if errors.Is(err, repository.ErrUnauthorized) {
				apiError(c, 401, "invalid_api_key", "a valid account API key is required")
				return
			}
			apiError(c, 500, "internal_error", "could not resolve API key")
			return
		}
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
		if account.TokenExpiresAt != nil && time.Now().After(*account.TokenExpiresAt) {
			apiError(c, 401, "token_expired", "account OAuth token has expired; manually import a new token")
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
		if err != nil {
			if errors.Is(err, errBodyTooLarge) {
				apiError(c, 413, "request_too_large", "request body exceeds configured limit")
			} else {
				apiError(c, 400, "invalid_request", err.Error())
			}
			return
		}
		if kind != routeModels && !json.Valid(body) {
			apiError(c, 400, "invalid_json", "request body must be valid JSON")
			return
		}
		if (kind == routeMessages || (kind == routeResponses && suffix == "")) && !hasStringModel(body) {
			apiError(c, 400, "invalid_request", "request body must include a non-empty model")
			return
		}

		credentials, err := s.repo.Credentials(c.Request.Context(), account.Account)
		if err != nil || credentials.Bearer() == "" {
			apiError(c, 502, "credential_error", "account credentials are unavailable")
			return
		}
		upstreamURL, err := s.upstreamURL(account.Provider, kind, suffix)
		if err != nil {
			apiError(c, 501, "unsupported_endpoint", err.Error())
			return
		}
		if c.Request.URL.RawQuery != "" {
			upstreamURL += "?" + c.Request.URL.RawQuery
		}

		release, acquired := s.tryAcquire(account.ID, account.ConcurrencyLimit)
		if !acquired {
			apiError(c, http.StatusTooManyRequests, "concurrency_limited", "account concurrency limit exceeded")
			return
		}
		defer release()
		upstreamRequest, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, upstreamURL, bytes.NewReader(body))
		if err != nil {
			apiError(c, 500, "internal_error", "could not build upstream request")
			return
		}
		copyDownstreamHeaders(upstreamRequest.Header, c.Request.Header)
		injectProviderHeaders(upstreamRequest.Header, account.Provider, account.AuthType, credentials)

		response, err := s.clients[string(account.Provider)].Do(upstreamRequest)
		if err != nil {
			s.repo.RecordUsage(c.Request.Context(), account.ID, account.Provider, c.Request.URL.Path, 502, started, "")
			apiError(c, 502, "upstream_unavailable", "could not connect to upstream provider")
			return
		}
		defer response.Body.Close()
		contentType := strings.ToLower(response.Header.Get("Content-Type"))
		if strings.Contains(contentType, "text/event-stream") {
			c.Writer.Header().Set("X-Accel-Buffering", "no")
		}
		copyUpstreamHeaders(c.Writer.Header(), response.Header)
		requestID := firstNonEmpty(response.Header.Get("request-id"), response.Header.Get("x-request-id"))
		c.Status(response.StatusCode)
		if strings.Contains(contentType, "text/event-stream") {
			relaySSE(c.Writer, response.Body)
		} else {
			_, _ = io.Copy(c.Writer, response.Body)
		}
		s.repo.RecordUsage(c.Request.Context(), account.ID, account.Provider, c.Request.URL.Path, response.StatusCode, started, requestID)
	}
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

func injectProviderHeaders(header http.Header, provider model.Provider, authType string, credentials model.Credentials) {
	header.Set("Content-Type", "application/json")
	switch provider {
	case model.ProviderClaude:
		if authType == "api_key" {
			header.Set("x-api-key", credentials.Bearer())
		} else {
			header.Set("Authorization", "Bearer "+credentials.Bearer())
		}
		if header.Get("anthropic-version") == "" {
			header.Set("anthropic-version", "2023-06-01")
		}
		header.Set("Accept", "application/json")
		header.Set("User-Agent", "claude-cli/1.0 ai-unisub")
		header.Set("x-app", "cli")
	case model.ProviderCodex:
		header.Set("Authorization", "Bearer "+credentials.Bearer())
		header.Set("Accept", "text/event-stream")
		header.Set("User-Agent", "codex_cli_rs/ai-unisub")
		header.Set("originator", "codex_cli_rs")
		header.Set("version", "ai-unisub/0.1.0")
		header.Set("chatgpt-account-id", credentials.ChatGPTAccountID)
	case model.ProviderGrok:
		header.Set("Authorization", "Bearer "+credentials.Bearer())
		header.Set("Accept", "application/json, text/event-stream")
		header.Set("User-Agent", "grok-cli/ai-unisub")
		header.Set("X-Grok-Client-Version", "ai-unisub/0.1.0")
		header.Set("x-grok-client-identifier", "grok-cli")
		header.Set("X-Grok-Client-Mode", "interactive")
	}
}

var strippedRequestHeaders = map[string]bool{
	"authorization": true, "x-api-key": true, "x-goog-api-key": true, "cookie": true,
	"chatgpt-account-id": true, "host": true, "content-length": true, "connection": true,
	"proxy-connection": true, "keep-alive": true, "transfer-encoding": true, "upgrade": true,
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
