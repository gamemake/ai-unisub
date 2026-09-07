package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

const requestLogBodyMaxBytes = 1 << 20

var redactedLogHeaders = map[string]bool{
	"authorization":          true,
	"proxy-authorization":    true,
	"x-api-key":              true,
	"x-goog-api-key":         true,
	"cookie":                 true,
	"set-cookie":             true,
	"chatgpt-account-id":     true,
	"x-xai-token-auth":       true,
	"x-authenticateresponse": true,
}

type capturingResponseWriter struct {
	gin.ResponseWriter
	stored limitedBuffer
	usage  sseUsageParser
	tail   tailBuffer
}

func newCapturingWriter(inner gin.ResponseWriter, max int) *capturingResponseWriter {
	if max <= 0 {
		max = requestLogBodyMaxBytes
	}
	return &capturingResponseWriter{
		ResponseWriter: inner,
		stored:         limitedBuffer{max: max},
		tail:           tailBuffer{max: 256 << 10},
	}
}

func (w *capturingResponseWriter) Write(p []byte) (int, error) {
	w.capture(p)
	return w.ResponseWriter.Write(p)
}

func (w *capturingResponseWriter) WriteString(s string) (int, error) {
	w.capture([]byte(s))
	return w.ResponseWriter.WriteString(s)
}

func (w *capturingResponseWriter) capture(p []byte) {
	w.stored.Write(p)
	w.tail.Write(p)
	w.usage.Write(p)
}

func (w *capturingResponseWriter) Body() string {
	return bytesToLogString(w.stored.buf)
}

func (w *capturingResponseWriter) Truncated() bool {
	return w.stored.truncated
}

func (w *capturingResponseWriter) TokenUsage() tokenUsage {
	w.usage.Flush()
	return mergeTokenUsage(w.usage.usage, parseJSONUsage(w.stored.buf), parseJSONUsage(w.tail.bytes()), parseSSEUsage(w.tail.bytes()))
}

type limitedBuffer struct {
	buf       []byte
	max       int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) {
	if b.max <= 0 {
		b.truncated = true
		return
	}
	remaining := b.max - len(b.buf)
	if remaining <= 0 {
		b.truncated = true
		return
	}
	if len(p) > remaining {
		b.buf = append(b.buf, p[:remaining]...)
		b.truncated = true
		return
	}
	b.buf = append(b.buf, p...)
}

type tailBuffer struct {
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) {
	if t.max <= 0 {
		return
	}
	if len(p) >= t.max {
		t.buf = append(t.buf[:0], p[len(p)-t.max:]...)
		return
	}
	need := len(t.buf) + len(p) - t.max
	if need > 0 {
		t.buf = append(t.buf[:0], t.buf[need:]...)
	}
	t.buf = append(t.buf, p...)
}

func (t *tailBuffer) bytes() []byte {
	return t.buf
}

func truncateForLog(body []byte, max int) (string, bool) {
	if max <= 0 {
		max = requestLogBodyMaxBytes
	}
	if len(body) <= max {
		return bytesToLogString(body), false
	}
	return bytesToLogString(body[:max]), true
}

func bytesToLogString(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	if utf8.Valid(body) {
		return string(body)
	}
	return strings.ToValidUTF8(string(body), "\uFFFD")
}

func sanitizeHeadersJSON(header http.Header) string {
	if len(header) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(header))
	for key := range header {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make(map[string][]string, len(keys))
	for _, key := range keys {
		if redactedLogHeaders[strings.ToLower(key)] {
			out[key] = []string{"[redacted]"}
			continue
		}
		out[key] = append([]string(nil), header[key]...)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

// doHTTP performs an outbound HTTP request and logs method, sanitized URL, status, and duration.
func doHTTP(client *http.Client, request *http.Request) (*http.Response, error) {
	if client == nil {
		client = http.DefaultClient
	}
	started := time.Now()
	response, err := client.Do(request)
	logOutboundHTTP(request, response, err, time.Since(started))
	return response, err
}

func logOutboundHTTP(request *http.Request, response *http.Response, err error, duration time.Duration) {
	attrs := []any{
		"method", request.Method,
		"url", sanitizeOutboundURL(request.URL),
		"duration_ms", duration.Milliseconds(),
	}
	if response != nil {
		attrs = append(attrs, "status", response.StatusCode)
	}
	if err != nil {
		attrs = append(attrs, "error", err)
		slog.Warn("http_outbound", attrs...)
		return
	}
	if response != nil && response.StatusCode >= 400 {
		slog.Warn("http_outbound", attrs...)
		return
	}
	slog.Info("http_outbound", attrs...)
}

func sanitizeOutboundURL(raw *url.URL) string {
	if raw == nil {
		return ""
	}
	cloned := *raw
	if raw.User != nil {
		cloned.User = url.User("[redacted]")
	}
	if cloned.RawQuery != "" {
		query := cloned.Query()
		changed := false
		for key := range query {
			if sensitiveQueryParam(key) {
				query.Set(key, "[redacted]")
				changed = true
			}
		}
		if changed {
			cloned.RawQuery = query.Encode()
		}
	}
	return cloned.String()
}

func sensitiveQueryParam(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	switch lower {
	case "code", "client_secret", "access_token", "refresh_token", "id_token", "password", "secret", "token":
		return true
	}
	return strings.HasSuffix(lower, "_token") || strings.HasSuffix(lower, "_secret") || strings.HasSuffix(lower, "_password")
}
