package aiprovider

import (
	"strings"
	"testing"
)

func TestStreamUsageAfterTraceLimitAndAcrossChunks(t *testing.T) {
	trace := &AIProviderCallTrace{}
	capture := &streamCapture{trace: trace, sse: true}
	for i := 0; i < 2048; i++ {
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
