package server

import "testing"

func TestParseJSONUsageClaudeAndResponses(t *testing.T) {
	claude := parseJSONUsage([]byte(`{"model":"claude-sonnet","usage":{"input_tokens":11,"output_tokens":7,"cache_read_input_tokens":3,"cache_creation_input_tokens":2}}`))
	if claude.Model != "claude-sonnet" || *claude.Input != 11 || *claude.Output != 7 || *claude.CacheRead != 3 || *claude.CacheCreation != 2 {
		t.Fatalf("claude usage = %+v", claude)
	}
	responses := parseJSONUsage([]byte(`{"response":{"model":"gpt-test","usage":{"input_tokens":20,"output_tokens":5,"total_tokens":25,"input_tokens_details":{"cached_tokens":4}}}}`))
	if responses.Model != "gpt-test" || *responses.Input != 20 || *responses.Output != 5 || *responses.CacheRead != 4 || *responses.Total != 25 {
		t.Fatalf("responses usage = %+v", responses)
	}
}

func TestParseSSEUsageMergesMessageStartAndDelta(t *testing.T) {
	body := []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-haiku\",\"usage\":{\"input_tokens\":9}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":4}}\n\n")
	usage := parseSSEUsage(body)
	if usage.Model != "claude-haiku" || usage.Input == nil || *usage.Input != 9 || usage.Output == nil || *usage.Output != 4 {
		t.Fatalf("sse usage = %+v", usage)
	}
}
