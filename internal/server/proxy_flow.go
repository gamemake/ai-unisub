package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
	"github.com/ai-unisub/ai-unisub/internal/repository"
	"github.com/gin-gonic/gin"
)

// proxyRequestLog holds mutable request-log state for one proxied call.
type proxyRequestLog struct {
	log     repository.RequestLog
	capture *capturingResponseWriter
	body    []byte
}

func (s *Server) beginProxyRequestLog(c *gin.Context) *proxyRequestLog {
	capture := newCapturingWriter(c.Writer, requestLogBodyMaxBytes)
	c.Writer = capture
	return &proxyRequestLog{
		capture: capture,
		log: repository.RequestLog{
			Method:         c.Request.Method,
			Path:           c.Request.URL.Path,
			Query:          c.Request.URL.RawQuery,
			ClientIP:       c.ClientIP(),
			StartedAt:      time.Now(),
			RequestHeaders: headersJSON(c.Request.Header),
		},
	}
}

func (s *Server) finalizeProxyRequestLog(c *gin.Context, rl *proxyRequestLog) {
	rl.log.FinishedAt = time.Now()
	rl.log.StatusCode = c.Writer.Status()
	if errorType, ok := c.Get("error_type"); ok {
		rl.log.ErrorType, _ = errorType.(string)
	}
	if c.Request.Context().Err() != nil && rl.log.StatusCode < 400 {
		rl.log.StatusCode = 499
		rl.log.ErrorType = "client_canceled"
	}
	rl.log.RequestBody, rl.log.RequestTruncated = truncateForLog(rl.body, requestLogBodyMaxBytes)
	rl.log.ResponseHeaders = headersJSON(c.Writer.Header())
	rl.log.ResponseBody = rl.capture.Body()
	rl.log.ResponseTruncated = rl.capture.Truncated()
	tokens := rl.capture.TokenUsage()
	if rl.log.Model == "" {
		rl.log.Model = firstNonEmpty(tokens.Model, requestModel(rl.body))
	}
	rl.log.InputTokens = tokens.Input
	rl.log.OutputTokens = tokens.Output
	rl.log.CacheReadTokens = tokens.CacheRead
	rl.log.CacheCreationTokens = tokens.CacheCreation
	rl.log.TotalTokens = tokens.Total
	if err := s.repo.RecordRequest(time.Local, rl.log); err != nil {
		slog.Error("record request failed", "method", rl.log.Method, "path", rl.log.Path, "error", err)
	}
}

func (rl *proxyRequestLog) bindAccount(account model.ResolvedSubscription) {
	rl.log.SubscriptionID = &account.ID
	rl.log.APIKeyID = &account.APIKeyID
	rl.log.UserID = account.UserID
	rl.log.Provider = string(account.Provider)
}

// resolveProxyAccount authenticates the downstream key, checks provider/route fit,
// loads credentials, and enforces RPM. On failure it writes the API error and returns ok=false.
func (s *Server) resolveProxyAccount(c *gin.Context, expectedProvider string, kind routeKind, rl *proxyRequestLog) (model.ResolvedSubscription, model.Credentials, bool) {
	plaintextKey := downstreamAPIKey(c.Request.Header)
	account, err := s.repo.ResolveAPIKey(c.Request.Context(), plaintextKey)
	if err != nil {
		if errors.Is(err, repository.ErrUnauthorized) {
			apiError(c, 401, "invalid_api_key", "a valid account API key is required")
			return model.ResolvedSubscription{}, model.Credentials{}, false
		}
		apiError(c, 500, "internal_error", "could not resolve API key")
		return model.ResolvedSubscription{}, model.Credentials{}, false
	}
	rl.bindAccount(account)
	if expectedProvider != "" && string(account.Provider) != expectedProvider {
		apiError(c, 403, "provider_mismatch", "API key is bound to a different provider")
		return model.ResolvedSubscription{}, model.Credentials{}, false
	}
	if kind == routeMessages || kind == routeCountTokens {
		if account.Provider != model.ProviderClaude {
			apiError(c, 403, "provider_mismatch", "Anthropic endpoints require a Claude account key")
			return model.ResolvedSubscription{}, model.Credentials{}, false
		}
	}
	if kind == routeResponses && account.Provider == model.ProviderClaude {
		apiError(c, 403, "provider_mismatch", "Responses endpoints require a Codex or Grok account key")
		return model.ResolvedSubscription{}, model.Credentials{}, false
	}
	credentials, err := s.repo.Credentials(c.Request.Context(), account.Subscription)
	if err != nil || credentials.Bearer() == "" {
		apiError(c, 502, "credential_error", "account credentials are unavailable")
		return model.ResolvedSubscription{}, model.Credentials{}, false
	}
	credentials, err = s.credentialsForRequest(c.Request.Context(), account.Subscription, credentials, false)
	if err != nil {
		apiError(c, 401, "reauth_required", err.Error())
		return model.ResolvedSubscription{}, model.Credentials{}, false
	}
	if account.RPMLimit != nil && !s.allowRate(fmt.Sprintf("api:%d", account.APIKeyID), *account.RPMLimit, time.Minute) {
		c.Header("Retry-After", "60")
		apiError(c, http.StatusTooManyRequests, "rate_limited", "API key requests-per-minute limit exceeded")
		return model.ResolvedSubscription{}, model.Credentials{}, false
	}
	return account, credentials, true
}

// readAndValidateProxyBody reads the request body, validates JSON/model when required,
// and stores the body on rl for request logging.
func (s *Server) readAndValidateProxyBody(c *gin.Context, kind routeKind, pathParam string, rl *proxyRequestLog) (body []byte, suffix string, ok bool) {
	if pathParam != "" {
		suffix = c.Param(pathParam)
		if err := validateResponseSubpath(suffix, c.Request.URL.EscapedPath()); err != nil {
			apiError(c, 404, "invalid_subpath", "Responses subpath is not allowed")
			return nil, "", false
		}
	}
	body, err := readBody(c.Request, s.cfg.MaxRequestBodyBytes)
	rl.body = body
	if err != nil {
		if errors.Is(err, errBodyTooLarge) {
			apiError(c, 413, "request_too_large", "request body exceeds configured limit")
		} else {
			apiError(c, 400, "invalid_request", err.Error())
		}
		return nil, "", false
	}
	rl.log.Model = requestModel(body)
	if kind != routeModels && !json.Valid(body) {
		apiError(c, 400, "invalid_json", "request body must be valid JSON")
		return nil, "", false
	}
	if (kind == routeMessages || (kind == routeResponses && suffix == "")) && !hasStringModel(body) {
		apiError(c, 400, "invalid_request", "request body must include a non-empty model")
		return nil, "", false
	}
	return body, suffix, true
}

func (s *Server) resolveProxyUpstreamURL(c *gin.Context, provider model.Provider, kind routeKind, suffix string) (string, bool) {
	upstreamURL, err := s.upstreamURL(provider, kind, suffix)
	if err != nil {
		apiError(c, 501, "unsupported_endpoint", err.Error())
		return "", false
	}
	upstreamURL = appendRawQuery(upstreamURL, c.Request.URL.RawQuery)
	if provider == model.ProviderClaude && (kind == routeMessages || kind == routeCountTokens) {
		upstreamURL = ensureQueryParam(upstreamURL, "beta", "true")
	}
	return upstreamURL, true
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

type proxyForwardInput struct {
	account                model.ResolvedSubscription
	credentials            model.Credentials
	kind                   routeKind
	upstreamURL            string
	body                   []byte
	clientHadContentLength bool
	requestLog             *repository.RequestLog
}

func (s *Server) buildUpstreamHTTPRequest(c *gin.Context, in proxyForwardInput, credentials model.Credentials) (*http.Request, error) {
	request, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, in.upstreamURL, bytes.NewReader(in.body))
	if err != nil {
		return nil, err
	}
	copyDownstreamHeaders(request.Header, c.Request.Header)
	injectProviderHeaders(request.Header, in.account.Provider, credentials, in.upstreamURL, in.kind)
	// Blind-copy strips Content-Length; restore a corrected value when the client sent one.
	if in.clientHadContentLength {
		request.Header.Set("Content-Length", strconv.Itoa(len(in.body)))
	}
	if in.requestLog != nil {
		in.requestLog.UpstreamRequestHeaders = headersJSON(request.Header)
	}
	return request, nil
}

// forwardUpstreamWithRefresh sends the upstream request and, on OAuth 401, refreshes
// credentials once and retries. On failure it writes the API error and returns ok=false.
func (s *Server) forwardUpstreamWithRefresh(c *gin.Context, client *http.Client, in proxyForwardInput) (*http.Response, bool) {
	credentials := in.credentials
	upstreamRequest, err := s.buildUpstreamHTTPRequest(c, in, credentials)
	if err != nil {
		apiError(c, 500, "internal_error", "could not build upstream request")
		return nil, false
	}
	response, err := doHTTP(client, upstreamRequest)
	if err != nil {
		apiError(c, 502, "upstream_unavailable", "could not connect to upstream provider")
		return nil, false
	}
	if response.StatusCode == http.StatusUnauthorized && credentials.RefreshToken != "" {
		response.Body.Close()
		credentials, err = s.credentialsForRequest(c.Request.Context(), in.account.Subscription, credentials, true)
		if err != nil {
			apiError(c, http.StatusUnauthorized, "reauth_required", err.Error())
			return nil, false
		}
		upstreamRequest, err = s.buildUpstreamHTTPRequest(c, in, credentials)
		if err != nil {
			apiError(c, 500, "internal_error", "could not retry upstream request")
			return nil, false
		}
		response, err = doHTTP(client, upstreamRequest)
		if err != nil {
			apiError(c, 502, "upstream_unavailable", "could not connect to upstream provider after refreshing credentials")
			return nil, false
		}
	}
	return response, true
}

func (s *Server) writeProxyUpstreamResponse(c *gin.Context, account model.ResolvedSubscription, response *http.Response, requestLog *repository.RequestLog) {
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
	if requestLog != nil {
		requestLog.RequestID = firstNonEmpty(response.Header.Get("request-id"), response.Header.Get("x-request-id"))
	}
	c.Status(response.StatusCode)
	if strings.Contains(contentType, "text/event-stream") {
		relaySSE(c.Writer, response.Body)
	} else {
		_, _ = io.Copy(c.Writer, response.Body)
	}
}
