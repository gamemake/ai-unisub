package aiprovider

import (
	"bytes"
	"encoding/json"
)

type wireUsage struct {
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
type usageEnvelope struct {
	Model string     `json:"model"`
	Usage *wireUsage `json:"usage"`
}

func parseUsageJSON(trace *AIProviderCallTrace, data []byte) {
	var value struct {
		usageEnvelope
		Response *usageEnvelope `json:"response"`
		Message  *usageEnvelope `json:"message"`
	}
	if json.Unmarshal(data, &value) != nil {
		return
	}
	event := value.usageEnvelope
	if value.Response != nil {
		event = *value.Response
	}
	if value.Message != nil {
		event = *value.Message
	}
	if event.Model != "" {
		trace.Model = event.Model
	}
	if u := event.Usage; u != nil {
		trace.InputTokens = max(trace.InputTokens, u.Input, u.Prompt)
		trace.OutputTokens = max(trace.OutputTokens, u.Output, u.Completion)
		trace.CacheCreationTokens = max(trace.CacheCreationTokens, u.CacheCreation)
		trace.CacheReadTokens = max(trace.CacheReadTokens, u.CacheRead, u.InputDetails.Cached, u.PromptDetails.Cached)
	}
}
func parseUsage(trace *AIProviderCallTrace) {
	if trace.Model == "" {
		var request usageEnvelope
		_ = json.Unmarshal(trace.RequestBody, &request)
		trace.Model = request.Model
	}
	parseUsageJSON(trace, trace.ResponseBody)
	for _, line := range bytes.Split(trace.ResponseBody, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("data:")) {
			parseUsageJSON(trace, bytes.TrimSpace(line[5:]))
		}
	}
}

// Read usage events throughout a stream, including after the trace body cap.
// A single event line is bounded as well, so malformed streams cannot grow RAM.
type streamCapture struct {
	limitedCapture
	trace    *AIProviderCallTrace
	sse      bool
	pending  []byte
	dropping bool
}

func (s *streamCapture) Write(p []byte) (int, error) {
	n, _ := s.limitedCapture.Write(p)
	if !s.sse {
		return n, nil
	}
	for len(p) > 0 {
		end := bytes.IndexByte(p, '\n')
		part := p
		if end >= 0 {
			part = p[:end]
		}
		if !s.dropping {
			if len(s.pending)+len(part) > 1<<20 {
				s.pending = nil
				s.dropping = true
			} else {
				s.pending = append(s.pending, part...)
			}
		}
		if end < 0 {
			break
		}
		if !s.dropping && bytes.HasPrefix(s.pending, []byte("data:")) {
			parseUsageJSON(s.trace, bytes.TrimSpace(s.pending[5:]))
		}
		s.pending = s.pending[:0]
		s.dropping = false
		p = p[end+1:]
	}
	return n, nil
}
