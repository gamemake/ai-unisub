package server

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
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
