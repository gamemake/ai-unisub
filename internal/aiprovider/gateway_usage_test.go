package aiprovider

import (
	"ai-unisub/internal/database"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPopulateGatewayUsageGzipEncodedSSE(t *testing.T) {
	var body bytes.Buffer
	writer := gzip.NewWriter(&body)
	_, _ = writer.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-opus\",\"usage\":{\"input_tokens\":94,\"cache_creation_input_tokens\":273,\"cache_read_input_tokens\":71352,\"output_tokens\":4}}}\n\n"))
	_, _ = writer.Write([]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":94,\"cache_creation_input_tokens\":273,\"cache_read_input_tokens\":71352,\"output_tokens\":82}}\n\n"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	headers := http.Header{"Content-Encoding": []string{"gzip"}}
	trace := &database.PersistedCallTrace{ResponseBody: decodeGatewayResponseBody(headers, body.Bytes()), ResponseHeaders: headers}
	if !bytes.HasPrefix(trace.ResponseBody, []byte("event: message_start")) {
		t.Fatalf("response body was not decoded: %q", trace.ResponseBody)
	}
	populateGatewayUsage(trace)
	if trace.Model != "claude-opus" || trace.InputTokens != 94 || trace.OutputTokens != 82 || trace.CacheCreationTokens != 273 || trace.CacheReadTokens != 71352 {
		t.Fatalf("gzip usage was not parsed: %+v", trace)
	}
}

func TestPopulateGatewayUsageOpenAIResponses(t *testing.T) {
	trace := &database.PersistedCallTrace{ResponseBody: []byte("data: {\"type\":\"response.completed\",\"response\":{\"model\":\"gpt-5\",\"usage\":{\"input_tokens\":100,\"output_tokens\":20,\"input_tokens_details\":{\"cached_tokens\":50}}}}\n\n")}
	populateGatewayUsage(trace)
	if trace.Model != "gpt-5" || trace.InputTokens != 100 || trace.OutputTokens != 20 || trace.CacheReadTokens != 50 {
		t.Fatalf("responses usage was not parsed: %+v", trace)
	}
}

type failingResponseWriter struct{ header http.Header }

func (w *failingResponseWriter) Header() http.Header       { return w.header }
func (w *failingResponseWriter) WriteHeader(int)           {}
func (w *failingResponseWriter) Write([]byte) (int, error) { return 0, errors.New("client gone") }

func TestStreamGatewayResponseStopsOnClientWriteFailure(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 100<<10)
	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(payload))}
	body, err := streamGatewayResponse(&failingResponseWriter{header: http.Header{}}, response)
	if !errors.Is(err, errGatewayClientWrite) {
		t.Fatalf("err = %v, want errGatewayClientWrite", err)
	}
	if len(body) >= len(payload) {
		t.Fatalf("recorded %d bytes, want streaming to stop before %d", len(body), len(payload))
	}
}

func TestGatewayCanceledContextRecordsClientCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for name, err := range map[string]error{
		"limiter":  gatewayLimiterError(context.Canceled),
		"upstream": gatewayUpstreamError(ctx, errors.New("read body")),
	} {
		typed, ok := errors.AsType[*gatewayError](err)
		if !ok || typed.status != statusClientClosedRequest || typed.message != MessageClientCanceled {
			t.Errorf("%s: got %v, want client canceled", name, err)
		}
	}
}

func TestGatewayTraceRecordsClientCanceledStatus(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	call := &gatewayCall{
		account: &Account{ID: 1}, supplier: newSupplierDummy(nil), startedAt: time.Now(),
		response:   &http.Response{StatusCode: http.StatusOK, Header: http.Header{}},
		requestErr: newGatewayError(statusClientClosedRequest, MessageClientCanceled, errGatewayClientWrite),
	}
	trace := call.trace(req)
	if trace.HTTPErrorCode != statusClientClosedRequest || trace.HTTPErrorInfo != MessageClientCanceled {
		t.Fatalf("trace status = %d %q, want 499 client canceled", trace.HTTPErrorCode, trace.HTTPErrorInfo)
	}
}
