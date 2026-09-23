package aiprovider

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

var errStreamCompleted = errors.New("stream completed")

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
	body := usageResponseBody(trace)
	parseUsageJSON(trace, body)
	for line := range bytes.SplitSeq(body, []byte("\n")) {
		if data, ok := bytes.CutPrefix(line, []byte("data:")); ok {
			parseUsageJSON(trace, bytes.TrimSpace(data))
		}
	}
}

// usageResponseBody decodes content encodings that prevent the response body
// from being inspected as JSON/SSE. The original body remains untouched in the
// trace because it is also used for forwarding and call-record persistence.
func usageResponseBody(trace *AIProviderCallTrace) []byte {
	if trace == nil || len(trace.ResponseBody) == 0 {
		return nil
	}
	encodings := strings.Split(trace.ResponseHeaders.Get("Content-Encoding"), ",")
	for i := len(encodings) - 1; i >= 0; i-- {
		if !strings.EqualFold(strings.TrimSpace(encodings[i]), "gzip") {
			continue
		}
		reader, err := gzip.NewReader(bytes.NewReader(trace.ResponseBody))
		if err != nil {
			return trace.ResponseBody
		}
		decoded, err := io.ReadAll(io.LimitReader(reader, 4<<20))
		_ = reader.Close()
		if err != nil {
			return trace.ResponseBody
		}
		return decoded
	}
	return trace.ResponseBody
}

// Read usage events throughout a stream, including after the trace body cap.
// A single event line is bounded as well, so malformed streams cannot grow RAM.
type streamCapture struct {
	limitedCapture
	trace     *AIProviderCallTrace
	sse       bool
	pending   []byte
	dropping  bool
	completed bool
}

func (s *streamCapture) Write(p []byte) (int, error) {
	n, _ := s.limitedCapture.Write(p)
	if !s.sse {
		return n, nil
	}
	for len(p) > 0 {
		part, rest, found := bytes.Cut(p, []byte("\n"))
		if !s.dropping {
			if len(s.pending)+len(part) > 1<<20 {
				s.pending = nil
				s.dropping = true
			} else {
				s.pending = append(s.pending, part...)
			}
		}
		if !found {
			break
		}
		if !s.dropping {
			if data, ok := bytes.CutPrefix(s.pending, []byte("data:")); ok {
				data = bytes.TrimSpace(data)
				parseUsageJSON(s.trace, data)
				if responseCompleted(data) {
					s.completed = true
					return n, errStreamCompleted
				}
			}
		}
		s.pending = s.pending[:0]
		s.dropping = false
		p = rest
	}
	return n, nil
}

func responseCompleted(data []byte) bool {
	var event struct {
		Type     string `json:"type"`
		Response struct {
			Status string          `json:"status"`
			Error  json.RawMessage `json:"error"`
		} `json:"response"`
	}
	if json.Unmarshal(data, &event) != nil || event.Type != "response.completed" || event.Response.Status != "completed" {
		return false
	}
	errorData := bytes.TrimSpace(event.Response.Error)
	return len(errorData) == 0 || bytes.Equal(errorData, []byte("null"))
}
