package aiprovider2

import (
	"ai-unisub/internal/common"
	"ai-unisub/internal/database"
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxGatewayRequestBodyBytes = 64 << 20

// GatewayAuthListener authenticates a client API key and resolves the concrete
// account and supplier that should handle the request. Group accounts must be
// resolved to a non-group member before returning.
type GatewayAuthListener interface {
	OnAuthAndAccount(apiKey string, req *http.Request) (userID int, account *Account, supplier GatewaySupplierListener, err error)
	RecordCallTrace(trace *database.PersistedCallTrace) error
}

// GatewaySupplierListener supplies provider-specific access information and
// request/response transformations for the Gateway.
type GatewaySupplierListener interface {
	GetID() string
	GetAccess(ctx context.Context, account *Account, req *http.Request) (accessToken, apiBaseURL string, err error)
	PreRequest(account *Account, req *http.Request, body *[]byte) error
	DoRequest(ctx context.Context, account *Account, req *http.Request) (*http.Response, error)
	// PostResponse is called after the upstream response body has been fully
	// read. res.Body has been closed. Its returned error may be recorded, but
	// cannot change a response that has already been sent to the client.
	PostResponse(account *Account, res *http.Response, body []byte) error
}

// Gateway authenticates and forwards a client request. Implementations write
// the complete client-facing response to w and honor cancellation through
// req.Context().
type Gateway interface {
	Handle(w http.ResponseWriter, req *http.Request)
}

type gateway struct {
	auth GatewayAuthListener
}

type gatewayCall struct {
	userID   int
	apiKey   string
	account  *Account
	supplier GatewaySupplierListener

	requestBody   []byte
	outboundBody  []byte
	outbound      *http.Request
	response      *http.Response
	responseBody  []byte
	startedAt     time.Time
	queueDuration time.Duration
	requestErr    error
}

type gatewayError struct {
	status  int
	message string
	err     error
}

func (e *gatewayError) Error() string {
	return e.err.Error()
}

func (e *gatewayError) Unwrap() error {
	return e.err
}

// NewGateway creates a forwarding gateway backed by auth. The listener is a
// required dependency and is normally implemented by the provider manager.
func NewGateway(auth GatewayAuthListener) (Gateway, error) {
	if auth == nil {
		return nil, errGatewayAuthListenerRequired
	}
	return &gateway{auth: auth}, nil
}

// Handle orchestrates one complete client-to-upstream call. Detailed HTTP
// operations live in helpers so the request lifecycle remains visible here.
func (g *gateway) Handle(w http.ResponseWriter, req *http.Request) {
	startedAt := time.Now().UTC()
	call, err := g.authenticate(req)
	if err != nil {
		writeGatewayError(w, err)
		return
	}
	call.startedAt = startedAt
	defer func() {
		if err := g.auth.RecordCallTrace(call.trace(req)); err != nil {
			slog.ErrorContext(req.Context(), "record gateway call", "error", err, "account_id", call.account.ID)
		}
	}()

	if err := call.readRequestBody(req); err != nil {
		call.requestErr = err
		writeGatewayError(w, err)
		return
	}

	limiter := newAccountLimiter(call.account)
	queuedAt := time.Now()
	if err := limiter.Acquire(req.Context()); err != nil {
		mappedErr := gatewayLimiterError(err)
		call.requestErr = mappedErr
		writeGatewayError(w, mappedErr)
		return
	}
	call.queueDuration = time.Since(queuedAt)
	defer limiter.Release()

	if err := call.prepareOutbound(req.Context(), req); err != nil {
		call.requestErr = err
		writeGatewayError(w, err)
		return
	}

	response, err := call.supplier.DoRequest(req.Context(), call.account, call.outbound)
	if err != nil {
		mappedErr := gatewayUpstreamError(req.Context(), err)
		call.requestErr = mappedErr
		writeGatewayError(w, mappedErr)
		return
	}
	if response == nil {
		mappedErr := newGatewayError(http.StatusBadGateway, common.MessageUpstreamNoResponse, errUpstreamNilResponse)
		call.requestErr = mappedErr
		writeGatewayError(w, mappedErr)
		return
	}
	call.response = response

	responseBody, streamErr := streamGatewayResponse(w, response)
	call.responseBody = responseBody
	closeErr := response.Body.Close()
	if streamErr != nil || closeErr != nil {
		err := errors.Join(streamErr, closeErr)
		call.requestErr = err
		if req.Context().Err() == nil {
			slog.ErrorContext(req.Context(), "stream gateway response", "error", err, "account_id", call.account.ID)
		}
		return
	}

	if err := call.supplier.PostResponse(call.account, response, responseBody); err != nil {
		slog.ErrorContext(req.Context(), "process completed gateway response", "error", err, "account_id", call.account.ID)
	}
}

func (c *gatewayCall) trace(req *http.Request) *database.PersistedCallTrace {
	trace := &database.PersistedCallTrace{
		UserID: c.userID, APIKey: c.apiKey, AccountID: c.account.ID, AIProviderType: c.supplier.GetID(),
		RequestMethod: req.Method, URL: req.URL.RequestURI(), RequestBody: bytes.Clone(c.requestBody),
		OriginalRequestHeaders: redactedGatewayHeaders(req.Header), ResponseBody: bytes.Clone(c.responseBody),
		RequestBytes: int64(len(c.requestBody)), ResponseBytes: int64(len(c.responseBody)),
		QueueDurationMs: c.queueDuration.Milliseconds(), RequestDurationMs: time.Since(c.startedAt).Milliseconds(), FinishedAt: time.Now().UTC(),
		RequestID: req.Header.Get("X-Request-Id"), SessionID: gatewaySessionID(req.Header), SourceIP: gatewaySourceIP(req.RemoteAddr),
	}
	if c.outbound != nil {
		trace.OutboundURL = c.outbound.URL.String()
		trace.OutboundRequestHeaders = redactedGatewayHeaders(c.outbound.Header)
	}
	if c.response != nil {
		trace.HTTPErrorCode = c.response.StatusCode
		trace.ResponseHeaders = redactedGatewayHeaders(c.response.Header)
	}
	if c.requestErr != nil {
		trace.HTTPErrorInfo = common.MessageUpstreamRequestFailed
		if typed, ok := errors.AsType[*gatewayError](c.requestErr); ok {
			trace.HTTPErrorInfo = typed.message
		}
	}
	populateGatewayUsage(trace)
	if model := gatewayRequestModel(c.outboundBody); model != "" {
		trace.Model = model
	}
	return trace
}

func (g *gateway) authenticate(req *http.Request) (*gatewayCall, error) {
	if req == nil || req.URL == nil {
		return nil, newGatewayError(http.StatusBadRequest, common.MessageInvalidUpstreamEndpoint, errInvalidGatewayRequest)
	}
	apiKey := presentedGatewayAPIKey(req.Header)
	if apiKey == "" {
		return nil, newGatewayError(http.StatusUnauthorized, common.MessageUnauthorized, errClientAPIKeyRequired)
	}

	userID, account, supplier, err := g.auth.OnAuthAndAccount(apiKey, req)
	if err != nil {
		status := http.StatusInternalServerError
		message := common.MessageInternalServerError
		switch {
		case errors.Is(err, ErrGatewayUnauthorized):
			status, message = http.StatusUnauthorized, common.MessageUnauthorized
		case errors.Is(err, ErrGatewayForbidden):
			status, message = http.StatusForbidden, common.MessageForbidden
		case errors.Is(err, ErrUnavailable):
			status, message = http.StatusServiceUnavailable, common.MessageAIProviderUnavailable
		}
		return nil, newGatewayError(status, message, fmt.Errorf("authenticate gateway request: %w", err))
	}
	if account == nil || supplier == nil {
		return nil, newGatewayError(http.StatusServiceUnavailable, common.MessageAIProviderUnavailable, errGatewayAuthNoAccountOrSupplier)
	}
	account.mu.RLock()
	kind := account.Config.Kind
	account.mu.RUnlock()
	if kind == AccountGroup {
		return nil, newGatewayError(http.StatusServiceUnavailable, common.MessageAIProviderUnavailable, errGatewayAuthUnresolvedGroupAccount)
	}

	return &gatewayCall{userID: userID, apiKey: apiKey, account: account, supplier: supplier}, nil
}

func (c *gatewayCall) readRequestBody(req *http.Request) error {
	if req.Body == nil {
		return nil
	}
	body, readErr := io.ReadAll(io.LimitReader(req.Body, maxGatewayRequestBodyBytes+1))
	closeErr := req.Body.Close()
	if readErr != nil || closeErr != nil {
		return newGatewayError(http.StatusBadRequest, common.MessageInvalidJSONBody, errors.Join(readErr, closeErr))
	}
	if len(body) > maxGatewayRequestBodyBytes {
		return newGatewayError(http.StatusRequestEntityTooLarge, "request body is too large", errGatewayRequestBodyExceedsLimit)
	}
	c.requestBody = body
	return nil
}

func (c *gatewayCall) prepareOutbound(ctx context.Context, original *http.Request) error {
	accessToken, apiBaseURL, err := c.supplier.GetAccess(ctx, c.account, original)
	if err != nil {
		return gatewayUpstreamError(ctx, fmt.Errorf("get supplier access: %w", err))
	}
	if strings.TrimSpace(accessToken) == "" {
		return newGatewayError(http.StatusBadGateway, common.MessageUpstreamRequestFailed, errSupplierEmptyAccessToken)
	}
	target, err := gatewayUpstreamURL(apiBaseURL, original.URL)
	if err != nil {
		return newGatewayError(http.StatusBadGateway, common.MessageInvalidUpstreamEndpoint, err)
	}

	outbound := original.Clone(ctx)
	outbound.URL = target
	outbound.Host = target.Host
	outbound.RequestURI = ""
	removeHopByHopHeaders(outbound.Header)
	for _, name := range []string{"Authorization", "X-Api-Key", "Cookie", "Proxy-Authorization"} {
		outbound.Header.Del(name)
	}
	setGatewayAccessHeader(outbound, c.account, accessToken)

	body := bytes.Clone(c.requestBody)
	if err := c.supplier.PreRequest(c.account, outbound, &body); err != nil {
		return newGatewayError(http.StatusBadGateway, common.MessageUpstreamRequestFailed, fmt.Errorf("prepare supplier request: %w", err))
	}
	resetGatewayRequestBody(outbound, body)
	c.outboundBody = bytes.Clone(body)
	c.outbound = outbound
	return nil
}

func streamGatewayResponse(w http.ResponseWriter, response *http.Response) ([]byte, error) {
	headers := response.Header.Clone()
	removeHopByHopHeaders(headers)
	headers.Del("Set-Cookie")
	for name, values := range headers {
		w.Header()[name] = append([]string(nil), values...)
	}
	w.WriteHeader(response.StatusCode)

	streaming := strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream")
	flusher, canFlush := w.(http.Flusher)
	buffer := make([]byte, 32<<10)
	body := make([]byte, 0)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			body = append(body, buffer[:n]...)
			if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
				return body, writeErr
			}
			if streaming && canFlush {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return body, nil
			}
			return body, readErr
		}
	}
}

func gatewayUpstreamURL(baseURL string, incoming *url.URL) (*url.URL, error) {
	base, err := url.Parse(baseURL)
	if err != nil || base.Host == "" || base.User != nil || base.Scheme != "http" && base.Scheme != "https" {
		return nil, errInvalidUpstreamEndpoint
	}
	if incoming == nil {
		return nil, errRequestURLRequired
	}

	suffix := strings.TrimPrefix(incoming.EscapedPath(), "/v1")
	prefix := strings.TrimRight(base.EscapedPath(), "/")
	if prefix == "" {
		prefix = "/v1"
	}
	rawPath := prefix + suffix
	base.Path, err = url.PathUnescape(rawPath)
	if err != nil {
		return nil, fmt.Errorf("decode upstream path: %w", err)
	}
	base.RawPath = rawPath
	base.RawQuery = incoming.RawQuery
	base.Fragment = ""
	return base, nil
}

func resetGatewayRequestBody(req *http.Request, body []byte) {
	req.Header.Del("Content-Length")
	if len(body) == 0 {
		req.Body = http.NoBody
		req.ContentLength = 0
		req.GetBody = func() (io.ReadCloser, error) { return http.NoBody, nil }
		return
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
}

func setGatewayAccessHeader(req *http.Request, account *Account, accessToken string) {
	account.mu.RLock()
	kind := account.Config.Kind
	account.mu.RUnlock()
	if kind == AccountAPI && isClaudeRequest(req) {
		req.Header.Set("X-Api-Key", accessToken)
		return
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
}

func removeHopByHopHeaders(header http.Header) {
	for _, value := range header.Values("Connection") {
		for name := range strings.SplitSeq(value, ",") {
			header.Del(strings.TrimSpace(name))
		}
	}
	for _, name := range []string{"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Proxy-Connection", "Te", "Trailer", "Transfer-Encoding", "Upgrade"} {
		header.Del(name)
	}
}

func presentedGatewayAPIKey(header http.Header) string {
	parts := strings.Fields(header.Get("Authorization"))
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return parts[1]
	}
	return strings.TrimSpace(header.Get("X-Api-Key"))
}

func redactedGatewayHeaders(header http.Header) http.Header {
	redacted := header.Clone()
	for _, name := range []string{"Authorization", "X-Api-Key", "Cookie", "Set-Cookie", "Proxy-Authorization"} {
		if redacted.Get(name) != "" {
			redacted.Set(name, "[redacted]")
		}
	}
	return redacted
}

func gatewaySessionID(header http.Header) string {
	for _, name := range []string{"Session-Id", "X-Session-Id", "Conversation-Id", "X-Conversation-Id"} {
		if value := strings.TrimSpace(header.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

func gatewaySourceIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return host
	}
	return remoteAddr
}

type gatewayWireUsage struct {
	Input         int `json:"input_tokens"`
	Output        int `json:"output_tokens"`
	Prompt        int `json:"prompt_tokens"`
	Completion    int `json:"completion_tokens"`
	CacheCreation int `json:"cache_creation_input_tokens"`
	CacheRead     int `json:"cache_read_input_tokens"`
	InputDetails  struct {
		Cached int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	PromptDetails struct {
		Cached int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

type gatewayUsageEnvelope struct {
	Model string            `json:"model"`
	Usage *gatewayWireUsage `json:"usage"`
}

func populateGatewayUsage(trace *database.PersistedCallTrace) {
	var request gatewayUsageEnvelope
	if json.Unmarshal(trace.RequestBody, &request) == nil {
		trace.Model = request.Model
	}
	parseGatewayUsage(trace, trace.ResponseBody)
	for line := range bytes.SplitSeq(trace.ResponseBody, []byte("\n")) {
		if data, ok := bytes.CutPrefix(line, []byte("data:")); ok {
			parseGatewayUsage(trace, bytes.TrimSpace(data))
		}
	}
}

func gatewayRequestModel(body []byte) string {
	var request gatewayUsageEnvelope
	if json.Unmarshal(body, &request) != nil {
		return ""
	}
	return request.Model
}

func parseGatewayUsage(trace *database.PersistedCallTrace, data []byte) {
	var value struct {
		gatewayUsageEnvelope
		Response *gatewayUsageEnvelope `json:"response"`
		Message  *gatewayUsageEnvelope `json:"message"`
	}
	if json.Unmarshal(data, &value) != nil {
		return
	}
	event := value.gatewayUsageEnvelope
	if value.Response != nil {
		event = *value.Response
	}
	if value.Message != nil {
		event = *value.Message
	}
	if event.Model != "" {
		trace.Model = event.Model
	}
	if usage := event.Usage; usage != nil {
		trace.InputTokens = max(trace.InputTokens, usage.Input, usage.Prompt)
		trace.OutputTokens = max(trace.OutputTokens, usage.Output, usage.Completion)
		trace.CacheCreationTokens = max(trace.CacheCreationTokens, usage.CacheCreation)
		trace.CacheReadTokens = max(trace.CacheReadTokens, usage.CacheRead, usage.InputDetails.Cached, usage.PromptDetails.Cached)
	}
}

func gatewayLimiterError(err error) error {
	switch {
	case errors.Is(err, ErrQueueTimeout), errors.Is(err, context.DeadlineExceeded):
		return newGatewayError(http.StatusGatewayTimeout, common.MessageGatewayTimeout, err)
	case errors.Is(err, context.Canceled):
		return newGatewayError(http.StatusRequestTimeout, common.MessageGatewayTimeout, err)
	default:
		return newGatewayError(http.StatusServiceUnavailable, common.MessageAIProviderUnavailable, err)
	}
}

func gatewayUpstreamError(ctx context.Context, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return newGatewayError(http.StatusGatewayTimeout, common.MessageGatewayTimeout, err)
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return newGatewayError(http.StatusRequestTimeout, common.MessageGatewayTimeout, err)
	}
	return newGatewayError(http.StatusBadGateway, common.MessageUpstreamRequestFailed, err)
}

func newGatewayError(status int, message string, err error) error {
	return &gatewayError{status: status, message: message, err: err}
}

func writeGatewayError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := common.MessageInternalServerError
	if typed, ok := errors.AsType[*gatewayError](err); ok {
		status = typed.status
		message = typed.message
	}
	common.WriteError(w, status, message)
}
