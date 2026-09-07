package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func quotaFromResponseHeaders(existing json.RawMessage, header http.Header, checkedAt time.Time) (json.RawMessage, bool) {
	quota := map[string]any{}
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &quota)
	}

	found := false
	if requestQuota, ok := rateLimitQuota(header, "requests", checkedAt); ok {
		quota["request_quota"] = requestQuota
		found = true
	}
	if tokenQuota, ok := rateLimitQuota(header, "tokens", checkedAt); ok {
		quota["token_quota"] = tokenQuota
		found = true
	}
	if windows, ok := anthropicUnifiedWindowsFromHeaders(header); ok {
		quota["windows"] = mergeQuotaWindows(quota["windows"], windows)
		found = true
	}
	if !found {
		return nil, false
	}
	encoded, err := json.Marshal(quota)
	if err != nil {
		return nil, false
	}
	return encoded, true
}

func rateLimitQuota(header http.Header, resource string, checkedAt time.Time) (map[string]any, bool) {
	result := map[string]any{}
	found := false
	for field, suffix := range map[string]string{
		"limit":     "limit-" + resource,
		"remaining": "remaining-" + resource,
	} {
		raw := strings.TrimSpace(header.Get("x-ratelimit-" + suffix))
		if raw == "" {
			continue
		}
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			continue
		}
		result[field] = value
		found = true
	}
	if resetAt, ok := rateLimitResetAt(header.Get("x-ratelimit-reset-"+resource), checkedAt); ok {
		result["reset_at"] = resetAt
		found = true
	}
	return result, found
}

func rateLimitResetAt(raw string, checkedAt time.Time) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if duration, err := time.ParseDuration(raw); err == nil {
		return checkedAt.Add(duration).UTC().Format(time.RFC3339Nano), true
	}
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed.UTC().Format(time.RFC3339Nano), true
	}
	if timestamp, err := strconv.ParseFloat(raw, 64); err == nil {
		seconds := timestamp
		if seconds > 1e12 {
			seconds /= 1000
		}
		whole := int64(seconds)
		nanos := int64((seconds - float64(whole)) * float64(time.Second))
		return time.Unix(whole, nanos).UTC().Format(time.RFC3339Nano), true
	}
	return "", false
}

// anthropicUnifiedWindowsFromHeaders maps Anthropic OAuth response headers into
// the same windows[] shape used by /api/oauth/usage normalization.
func anthropicUnifiedWindowsFromHeaders(header http.Header) ([]any, bool) {
	mapping := []struct {
		headerWindow string
		name         string
	}{
		{"5h", "five_hour"},
		{"7d", "seven_day"},
		{"7d_oi", "seven_day_overage_included"},
	}
	windows := make([]any, 0, len(mapping))
	for _, item := range mapping {
		prefix := "anthropic-ratelimit-unified-" + item.headerWindow + "-"
		utilizationRaw := strings.TrimSpace(header.Get(prefix + "utilization"))
		resetRaw := strings.TrimSpace(header.Get(prefix + "reset"))
		if utilizationRaw == "" && resetRaw == "" {
			continue
		}
		window := map[string]any{"name": item.name}
		if utilizationRaw != "" {
			if utilization, err := strconv.ParseFloat(utilizationRaw, 64); err == nil {
				used := utilization
				if used <= 1 {
					used *= 100
				}
				used = clampPercent(used)
				window["used_percent"] = used
				window["remaining_percent"] = 100 - used
			}
		}
		if resetAt, ok := anthropicUnifiedResetAt(resetRaw); ok {
			window["reset_at"] = resetAt
		}
		_, hasUsed := window["used_percent"]
		_, hasReset := window["reset_at"]
		if !hasUsed && !hasReset {
			continue
		}
		windows = append(windows, window)
	}
	if len(windows) == 0 {
		return nil, false
	}
	return windows, true
}

func anthropicUnifiedResetAt(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed.UTC().Format(time.RFC3339Nano), true
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed.UTC().Format(time.RFC3339Nano), true
	}
	timestamp, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return "", false
	}
	if timestamp > 1e12 {
		timestamp /= 1000
	}
	return time.Unix(timestamp, 0).UTC().Format(time.RFC3339Nano), true
}

func mergeQuotaWindows(existing any, incoming []any) []any {
	byName := map[string]map[string]any{}
	order := make([]string, 0)
	appendWindow := func(item any) {
		window, ok := item.(map[string]any)
		if !ok {
			return
		}
		name, _ := window["name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, seen := byName[name]; !seen {
			order = append(order, name)
		}
		byName[name] = window
	}
	switch typed := existing.(type) {
	case []any:
		for _, item := range typed {
			appendWindow(item)
		}
	}
	for _, item := range incoming {
		appendWindow(item)
	}
	merged := make([]any, 0, len(order))
	for _, name := range order {
		merged = append(merged, byName[name])
	}
	return merged
}
