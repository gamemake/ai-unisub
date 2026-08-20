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
