package aiprovider

import (
	"ai-unisub/internal/database"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

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

func (c *gatewayCall) trace(req *http.Request) *database.PersistedCallTrace {
	trace := &database.PersistedCallTrace{
		UserID: c.userID, APIKey: c.apiKey, AccountID: c.account.ID, AIProviderType: c.supplier.GetID(),
		RequestMethod: req.Method, URL: req.URL.RequestURI(), RequestBody: c.requestBody,
		OriginalRequestHeaders: redactedGatewayHeaders(req.Header), ResponseBody: c.responseBody,
		RequestBytes: int64(len(c.requestBody)), ResponseBytes: int64(len(c.responseBody)),
		QueueDurationMs: c.queueDuration.Milliseconds(), RequestDurationMs: time.Since(c.startedAt).Milliseconds(), FinishedAt: time.Now().UTC(),
		RequestID: gatewayRequestID(req.Header, nil), SessionID: gatewaySessionID(req.Header), SourceIP: gatewaySourceIP(req.RemoteAddr),
	}
	if c.outbound != nil {
		trace.OutboundURL = c.outbound.URL.String()
		trace.OutboundRequestHeaders = redactedGatewayHeaders(c.outbound.Header)
	}
	if c.response != nil {
		trace.HTTPErrorCode = c.response.StatusCode
		trace.ResponseHeaders = redactedGatewayHeaders(c.response.Header)
		trace.RequestID = gatewayRequestID(req.Header, c.response.Header)
		trace.ResponseBody = decodeGatewayResponseBody(trace.ResponseHeaders, trace.ResponseBody)
	}
	if c.requestErr != nil {
		trace.HTTPErrorInfo = MessageUpstreamRequestFailed
		if typed, ok := errors.AsType[*gatewayError](c.requestErr); ok {
			trace.HTTPErrorInfo = typed.message
			// A client disconnect overrides the upstream status, which may be 200.
			if typed.status == statusClientClosedRequest {
				trace.HTTPErrorCode = statusClientClosedRequest
			}
		}
	}
	populateGatewayUsage(trace)
	if model := gatewayRequestModel(c.outboundBody); model != "" {
		trace.Model = model
	}
	return trace
}

func (c *gatewayCall) readRequestBody(req *http.Request) error {
	if req.Body == nil {
		return nil
	}
	body, readErr := io.ReadAll(io.LimitReader(req.Body, maxGatewayRequestBodyBytes+1))
	closeErr := req.Body.Close()
	if readErr != nil || closeErr != nil {
		return newGatewayError(http.StatusBadRequest, MessageInvalidJSONBody, errors.Join(readErr, closeErr))
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
		return newGatewayError(http.StatusBadGateway, MessageUpstreamRequestFailed, errSupplierEmptyAccessToken)
	}
	target, err := gatewayUpstreamURL(apiBaseURL, original.URL)
	if err != nil {
		return newGatewayError(http.StatusBadGateway, MessageInvalidUpstreamEndpoint, err)
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
		return newGatewayError(http.StatusBadGateway, MessageUpstreamRequestFailed, fmt.Errorf("prepare supplier request: %w", err))
	}
	resetGatewayRequestBody(outbound, body)
	c.outboundBody = bytes.Clone(body)
	c.outbound = outbound
	return nil
}
