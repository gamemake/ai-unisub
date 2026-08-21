package server

import (
	"bytes"
	"encoding/json"
	"strings"
)

type tokenUsage struct {
	Model         string
	Input         *int64
	Output        *int64
	CacheRead     *int64
	CacheCreation *int64
	Total         *int64
}

func mergeTokenUsage(parts ...tokenUsage) tokenUsage {
	var out tokenUsage
	for _, part := range parts {
		if part.Model != "" {
			out.Model = part.Model
		}
		if part.Input != nil {
			out.Input = part.Input
		}
		if part.Output != nil {
			out.Output = part.Output
		}
		if part.CacheRead != nil {
			out.CacheRead = part.CacheRead
		}
		if part.CacheCreation != nil {
			out.CacheCreation = part.CacheCreation
		}
		if part.Total != nil {
			out.Total = part.Total
		}
	}
	if out.Total == nil && out.Input != nil && out.Output != nil {
		total := *out.Input + *out.Output
		out.Total = &total
	}
	return out
}

func parseJSONUsage(body []byte) tokenUsage {
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '{' {
		return tokenUsage{}
	}
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return tokenUsage{}
	}
	return tokenUsageFromObject(payload)
}

func parseSSEUsage(body []byte) tokenUsage {
	var parser sseUsageParser
	parser.Write(body)
	parser.Flush()
	return parser.usage
}

type sseUsageParser struct {
	rest  []byte
	usage tokenUsage
}

func (p *sseUsageParser) Write(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	p.rest = append(p.rest, chunk...)
	for {
		i := bytes.IndexByte(p.rest, '\n')
		if i < 0 {
			return
		}
		line := bytes.TrimRight(p.rest[:i], "\r")
		p.rest = p.rest[i+1:]
		p.consumeLine(line)
	}
}

func (p *sseUsageParser) Flush() {
	if len(p.rest) == 0 {
		return
	}
	p.consumeLine(bytes.TrimRight(p.rest, "\r"))
	p.rest = nil
}

func (p *sseUsageParser) consumeLine(line []byte) {
	if !bytes.HasPrefix(line, []byte("data:")) && !bytes.HasPrefix(line, []byte("data: ")) {
		return
	}
	data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
	if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
		return
	}
	p.usage = mergeTokenUsage(p.usage, parseJSONUsage(data))
}

func tokenUsageFromObject(payload map[string]any) tokenUsage {
	var usage tokenUsage
	if model, ok := stringValue(payload["model"]); ok {
		usage.Model = model
	}
	if message, ok := payload["message"].(map[string]any); ok {
		usage = mergeTokenUsage(usage, tokenUsageFromObject(message))
	}
	if response, ok := payload["response"].(map[string]any); ok {
		usage = mergeTokenUsage(usage, tokenUsageFromObject(response))
	}
	rawUsage, _ := payload["usage"].(map[string]any)
	if rawUsage == nil {
		return usage
	}
	usage.Input = firstInt64(rawUsage, "input_tokens", "prompt_tokens")
	usage.Output = firstInt64(rawUsage, "output_tokens", "completion_tokens")
	usage.CacheRead = firstInt64(rawUsage, "cache_read_input_tokens", "cache_read_tokens")
	usage.CacheCreation = firstInt64(rawUsage, "cache_creation_input_tokens", "cache_creation_tokens")
	usage.Total = firstInt64(rawUsage, "total_tokens")
	if details, ok := rawUsage["input_tokens_details"].(map[string]any); ok {
		if cached := firstInt64(details, "cached_tokens", "cache_read_tokens"); cached != nil {
			usage.CacheRead = cached
		}
	}
	if details, ok := rawUsage["prompt_tokens_details"].(map[string]any); ok {
		if cached := firstInt64(details, "cached_tokens"); cached != nil {
			usage.CacheRead = cached
		}
	}
	return usage
}

func requestModel(body []byte) string {
	var envelope struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return ""
	}
	return strings.TrimSpace(envelope.Model)
}

func firstInt64(object map[string]any, keys ...string) *int64 {
	for _, key := range keys {
		if value, ok := int64Value(object[key]); ok {
			return &value
		}
	}
	return nil
}

func int64Value(value any) (int64, bool) {
	switch n := value.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	case json.Number:
		parsed, err := n.Int64()
		return parsed, err == nil
	case string:
		var parsed int64
		if _, err := json.Number(n).Int64(); err == nil {
			parsed, _ = json.Number(n).Int64()
			return parsed, true
		}
	}
	return 0, false
}

func stringValue(value any) (string, bool) {
	text, ok := value.(string)
	text = strings.TrimSpace(text)
	return text, ok && text != ""
}
