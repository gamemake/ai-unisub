package unisub

import (
	"ai-unisub/internal/aiprovider"
	"ai-unisub/internal/common"
	"ai-unisub/internal/database"
	"ai-unisub/internal/service"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type GatewayModule struct{}

func NewGatewayModule() *GatewayModule { return &GatewayModule{} }
func (m *GatewayModule) Name() string  { return "gateway" }
func (m *GatewayModule) Close() error  { return nil }
func (m *GatewayModule) Init(ctx service.ModuleContext) error {
	ctx.HandleFunc("/v1/", service.RouteOptions{Auth: service.AuthAPIKey, Name: "gateway"}, func(w http.ResponseWriter, r *http.Request) { m.handle(ctx, w, r) })
	return nil
}
func (m *GatewayModule) handle(ctx service.ModuleContext, w http.ResponseWriter, r *http.Request) {
	principal, ok := service.PrincipalFromContext(r.Context())
	if !ok || principal.Account == nil {
		common.WriteError(w, 401, common.MessageUnauthorized)
		return
	}
	if _, err := aiprovider.SessionID(r.Header); err != nil {
		common.WriteError(w, 400, err.Error())
		return
	}
	userID := ""
	if principal.User != nil {
		userID = principal.User.ID
	}
	timeout := ctx.Config().GatewayRequestTimeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	requestContext, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	selected, err := ctx.AIProviders().Select(principal.Account.ID, userID, r.Header, r.URL.Path, nil)
	if err != nil {
		status := 503
		if errors.Is(err, aiprovider.ErrClientDenied) {
			status = 403
		}
		common.WriteError(w, status, err.Error())
		return
	}
	var result *aiprovider.AIProviderCallTrace
	defer func() { ctx.AIProviders().ReportSelection(selected, result, requestContext.Err() != nil) }()
	config := selected.Provider.Config()
	endpoint := config.APIEndpoint
	if endpoint == "" && config.AuthType == aiprovider.AuthTypeAPIKey {
		endpoint = ctx.AIProviders().DefaultURL(config.Supplier, aiprovider.EndpointClient(r.URL.Path))
		if endpoint == "" && config.Supplier != "" {
			common.WriteError(w, 502, "supplier has no built-in URL for this protocol")
			return
		}
	}
	target, err := upstreamURL(endpoint, selected.Adapter, config.AuthType, r.URL)
	if err != nil {
		common.WriteError(w, 502, common.MessageInvalidUpstreamEndpoint)
		return
	}
	output := &gatewayWriter{ResponseWriter: w}
	outbound := r.Clone(aiprovider.WithResponseWriter(requestContext, output))
	outbound.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	var originalBody []byte
	{
		body, readErr := io.ReadAll(outbound.Body)
		_ = outbound.Body.Close()
		if readErr != nil {
			status := 400
			if _, ok := errors.AsType[*http.MaxBytesError](readErr); ok {
				status = 413
			}
			common.WriteError(w, status, "invalid request body")
			return
		}
		originalBody = append([]byte(nil), body...)
		outbound.Body = io.NopCloser(bytes.NewReader(body))
		outbound.ContentLength = int64(len(body))
		outbound.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	}
	outbound.URL, outbound.Host, outbound.RequestURI = target, target.Host, ""
	for _, value := range outbound.Header.Values("Connection") {
		for name := range strings.SplitSeq(value, ",") {
			outbound.Header.Del(strings.TrimSpace(name))
		}
	}
	for _, name := range []string{"Connection", "Keep-Alive", "Proxy-Authorization", "Proxy-Authenticate", "Te", "Trailer", "Transfer-Encoding", "Upgrade", "Cookie"} {
		outbound.Header.Del(name)
	}
	started := time.Now().UTC()
	recorded := false
	recorder := func(trace *aiprovider.AIProviderCallTrace) {
		if trace == nil {
			return
		}
		result = trace
		recorded = true
		if !output.written {
			status := cmp.Or(trace.ResponseStatus, trace.HTTPErrorCode)
			if status == 0 && trace.HTTPErrorInfo != "" {
				status = http.StatusBadGateway
			}
			status = cmp.Or(status, http.StatusOK)
			if trace.HTTPErrorInfo != "" && len(trace.ResponseBody) == 0 {
				common.WriteError(output, status, common.MessageUpstreamRequestFailed)
			} else {
				for key, values := range trace.ResponseHeaders {
					if !strings.EqualFold(key, "Set-Cookie") {
						output.Header()[key] = values
					}
				}
				output.WriteHeader(status)
				_, _ = output.Write(trace.ResponseBody)
			}
		}
		requestID, _ := service.RequestIDFromContext(r.Context())
		sourceIP, _, _ := net.SplitHostPort(r.RemoteAddr)
		apiKey := ""
		if parts := strings.Fields(r.Header.Get("Authorization")); len(parts) == 2 {
			apiKey = parts[1]
		}
		// The DB uses the presented key to associate historical records with users.
		// Headers shown in the UI do not contain downstream/upstream secrets.
		saved := &database.PersistedCallTrace{ID: newID(), APIKey: apiKey, AccountID: selected.ID, AIProviderType: selected.Adapter, RequestID: requestID, SourceIP: sourceIP, URL: r.URL.RequestURI(), HTTPErrorCode: trace.HTTPErrorCode, HTTPErrorInfo: "", OriginalRequestHeaders: redactedHeaders(r.Header), OutboundRequestHeaders: redactedHeaders(trace.OutboundRequestHeaders), RequestBody: trace.RequestBody, ResponseHeaders: redactedHeaders(trace.ResponseHeaders), ResponseBody: trace.ResponseBody, Model: trace.Model, InputTokens: trace.InputTokens, OutputTokens: trace.OutputTokens, CacheCreationTokens: trace.CacheCreationTokens, CacheReadTokens: trace.CacheReadTokens, StartedAt: started, FinishedAt: time.Now().UTC()}
		if output.status >= 400 {
			saved.HTTPErrorCode = output.status
		}
		saved.SessionID = callSessionID(r.Header)
		if trace.HTTPErrorInfo != "" {
			saved.HTTPErrorInfo = common.MessageUpstreamRequestFailed
		}
		if err := ctx.Database().RecordCallTrace(saved); err != nil {
			common.ModuleLogger("gateway").Error("record_call_failed", fmt.Sprintf("record gateway call %s: %v", requestID, err))
		}
	}
	var tried []string
	root, _ := ctx.AIProviders().Get(principal.Account.ID)
	for attempt := range 3 {
		var retryTrace *aiprovider.AIProviderCallTrace
		canSwitch := root != nil && root.Config().Kind == "group" && attempt < 2
		err = selected.Account.Handle(outbound, func(trace *aiprovider.AIProviderCallTrace) {
			if canSwitch && trace != nil && trace.RetrySafe && !output.written {
				retryTrace = trace
				return
			}
			recorder(trace)
		}, ctx.Config().GatewayQueueLimit)
		if !canSwitch || output.written || requestContext.Err() != nil || (retryTrace == nil && !errors.Is(err, aiprovider.ErrQueueFull) && !errors.Is(err, aiprovider.ErrQueueTimeout)) {
			break
		}
		tried = append(tried, selected.ID)
		ctx.AIProviders().ReportSelection(selected, retryTrace, false)
		next, selectErr := ctx.AIProviders().Select(principal.Account.ID, userID, r.Header, r.URL.Path, tried)
		if selectErr != nil {
			if retryTrace != nil {
				recorder(retryTrace)
				result = nil
			}
			break
		}
		selected = next
		result = nil
		config = selected.Provider.Config()
		endpoint = config.APIEndpoint
		if endpoint == "" && config.AuthType == aiprovider.AuthTypeAPIKey {
			endpoint = ctx.AIProviders().DefaultURL(config.Supplier, aiprovider.EndpointClient(r.URL.Path))
			if endpoint == "" && config.Supplier != "" {
				common.WriteError(w, 502, "supplier has no built-in URL for this protocol")
				return
			}
		}
		target, err = upstreamURL(endpoint, selected.Adapter, config.AuthType, r.URL)
		if err != nil {
			break
		}
		outbound.URL, outbound.Host = target, target.Host
		body := originalBody
		outbound.Body = io.NopCloser(bytes.NewReader(body))
		outbound.ContentLength = int64(len(body))
		outbound.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	}
	if err != nil && !output.written {
		status := http.StatusServiceUnavailable
		message := common.MessageAIProviderUnavailable
		switch {
		case errors.Is(err, aiprovider.ErrClientDenied):
			status = http.StatusForbidden
			message = err.Error()
		case errors.Is(err, aiprovider.ErrQueueFull):
			status = http.StatusTooManyRequests
			message = common.MessageAIProviderQueueFull
		case errors.Is(err, aiprovider.ErrQueueTimeout), errors.Is(err, context.DeadlineExceeded):
			status = http.StatusGatewayTimeout
			message = common.MessageGatewayTimeout
		case errors.Is(err, context.Canceled):
			return
		}
		common.WriteError(w, status, message)
		return
	}
	if !recorded && !output.written {
		common.WriteError(w, 502, common.MessageUpstreamNoResponse)
	}
}

// Session IDs identify upstream client conversations, not Dashboard login sessions.
func callSessionID(headers http.Header) string {
	session, _ := aiprovider.SessionID(headers)
	return session
}
func upstreamURL(endpoint, name, auth string, incoming *url.URL) (*url.URL, error) {
	if endpoint == "" {
		switch name {
		case "claude":
			endpoint = "https://api.anthropic.com/v1"
		case "grok":
			endpoint = "https://api.x.ai/v1"
		case "codex":
			endpoint = "https://api.openai.com/v1"
			if auth != aiprovider.AuthTypeAPIKey {
				endpoint = "https://chatgpt.com/backend-api/codex"
			}
		case "dummy":
			endpoint = "http://dummy.local/v1"
		default:
			return nil, errors.New("unknown provider")
		}
	}
	base, err := url.Parse(endpoint)
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") {
		return nil, errors.New("invalid endpoint")
	}
	// Base URLs commonly already end with /v1; append the official relative path
	// without duplicating the version segment, preserving escaped resource IDs.
	suffix := strings.TrimPrefix(incoming.EscapedPath(), "/v1")
	prefix := cmp.Or(strings.TrimRight(base.EscapedPath(), "/"), "/v1")
	rawPath := prefix + suffix
	base.Path, err = url.PathUnescape(rawPath)
	if err != nil {
		return nil, err
	}
	base.RawPath, base.RawQuery, base.Fragment = rawPath, incoming.RawQuery, ""
	return base, nil
}
func redactedHeaders(headers http.Header) http.Header {
	copy := headers.Clone()
	for _, name := range []string{"Authorization", "X-Api-Key", "Cookie", "Set-Cookie", "Proxy-Authorization"} {
		if copy.Get(name) != "" {
			copy.Set(name, "[redacted]")
		}
	}
	return copy
}

type gatewayWriter struct {
	http.ResponseWriter
	written bool
	status  int
}

func (w *gatewayWriter) WriteHeader(code int) {
	if w.written {
		return
	}
	w.written = true
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
func (w *gatewayWriter) Write(p []byte) (int, error) {
	if !w.written {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(p)
}
func (w *gatewayWriter) Flush() {
	if !w.written {
		w.WriteHeader(200)
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
