package aiprovider

import (
	"bytes"
	"compress/gzip"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestParseUsageGzipEncodedSSE(t *testing.T) {
	var body bytes.Buffer
	writer := gzip.NewWriter(&body)
	_, _ = writer.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-opus\",\"usage\":{\"input_tokens\":94,\"cache_creation_input_tokens\":273,\"cache_read_input_tokens\":71352,\"output_tokens\":4}}}\n\n"))
	_, _ = writer.Write([]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":94,\"cache_creation_input_tokens\":273,\"cache_read_input_tokens\":71352,\"output_tokens\":82}}\n\n"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	trace := &AIProviderCallTrace{ResponseBody: body.Bytes(), ResponseHeaders: http.Header{"Content-Encoding": []string{"gzip"}}}
	parseUsage(trace)
	if trace.Model != "claude-opus" || trace.InputTokens != 94 || trace.OutputTokens != 82 || trace.CacheCreationTokens != 273 || trace.CacheReadTokens != 71352 {
		t.Fatalf("gzip usage was not parsed: %+v", trace)
	}
}

func TestStreamUsageAfterTraceLimitAndAcrossChunks(t *testing.T) {
	trace := &AIProviderCallTrace{}
	capture := &streamCapture{trace: trace, sse: true}
	for range 2048 {
		_, _ = capture.Write([]byte("data: " + strings.Repeat("x", 1024) + "\n\n"))
	}
	for _, part := range []string{`data: {"response":{"model":"m","usage":{"input_tokens":100,`, `"output_tokens":20,"input_tokens_details":{"cached_tokens":50}}}}`, "\n\n"} {
		_, _ = capture.Write([]byte(part))
	}
	if capture.Len() != 1<<20 {
		t.Fatalf("trace cap=%d", capture.Len())
	}
	if trace.InputTokens != 100 || trace.OutputTokens != 20 || trace.CacheReadTokens != 50 || trace.Model != "m" {
		t.Fatalf("missing final stream usage: %+v", trace)
	}
}

func TestStreamCaptureStopsAfterCompletedResponse(t *testing.T) {
	const stream = "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"error\":null}}\n\n"
	capture := &streamCapture{trace: &AIProviderCallTrace{}, sse: true}
	n, err := capture.Write([]byte(stream))
	if n != len(stream) || !errors.Is(err, errStreamCompleted) || !capture.completed {
		t.Fatalf("capture result n=%d err=%v completed=%v", n, err, capture.completed)
	}
}
