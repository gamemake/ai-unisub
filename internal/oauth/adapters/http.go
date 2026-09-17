package adapters

import (
	"ai-unisub/internal/common"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

type HTTPCallMeta struct {
	Provider  string
	Operation string
}

func challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
func nowPlus(minutes int) time.Time { return time.Now().Add(time.Duration(minutes) * time.Minute) }

func defaultHTTPClient() *http.Client { return &http.Client{Timeout: 30 * time.Second} }

func clientOrDefault(base *http.Client) *http.Client {
	if base == nil {
		base = defaultHTTPClient()
	}
	return base
}

func clientOr(configured, supplied *http.Client) *http.Client {
	if supplied != nil {
		return supplied
	}
	return configured
}
func readResponseDo(client *http.Client, req *http.Request, meta HTTPCallMeta, out any) ([]byte, error) {
	started := time.Now()
	logger := common.ModuleLogger("oauth")
	if client == nil {
		client = http.DefaultClient
	}
	logger.DebugAttrs("http_request_started",
		slog.String("provider", meta.Provider),
		slog.String("operation", meta.Operation),
		slog.String("method", req.Method),
		slog.String("url", safeURL(req.URL)),
		slog.Int("request_bytes", requestBytes(req)),
	)
	resp, err := client.Do(req)
	if err != nil {
		logger.ErrorAttrs("http_request_failed",
			slog.String("provider", meta.Provider),
			slog.String("operation", meta.Operation),
			slog.String("method", req.Method),
			slog.String("url", safeURL(req.URL)),
			slog.Int("request_bytes", requestBytes(req)),
			slog.Int64("duration_ms", time.Since(started).Milliseconds()),
			slog.String("outcome", classifyHTTPError(req.Context(), err)),
		)
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil || len(body) > 1<<20 {
		logger.ErrorAttrs("http_request_failed",
			slog.String("provider", meta.Provider), slog.String("operation", meta.Operation),
			slog.String("method", req.Method), slog.String("url", safeURL(req.URL)),
			slog.Int("request_bytes", requestBytes(req)),
			slog.Int("status", resp.StatusCode), slog.Int64("duration_ms", time.Since(started).Milliseconds()),
			slog.String("outcome", "response_unreadable"),
		)
		return nil, errors.New("oauth response is too large or unreadable")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.Unmarshal(body, &e)
		if e.Error != "" {
			logger.WarnAttrs("http_request_failed",
				slog.String("provider", meta.Provider), slog.String("operation", meta.Operation),
				slog.String("method", req.Method), slog.String("url", safeURL(req.URL)),
				slog.Int("status", resp.StatusCode), slog.Int("response_bytes", len(body)),
				slog.Int64("duration_ms", time.Since(started).Milliseconds()), slog.String("outcome", "http_error"),
			)
			return nil, fmt.Errorf("oauth request failed: %s", e.Error)
		}
		logger.WarnAttrs("http_request_failed",
			slog.String("provider", meta.Provider), slog.String("operation", meta.Operation),
			slog.String("method", req.Method), slog.String("url", safeURL(req.URL)),
			slog.Int("status", resp.StatusCode), slog.Int("response_bytes", len(body)),
			slog.Int64("duration_ms", time.Since(started).Milliseconds()), slog.String("outcome", "http_error"),
		)
		return nil, fmt.Errorf("oauth request failed with status %d", resp.StatusCode)
	}
	if out != nil && len(body) > 0 {
		if err := json.Unmarshal(body, out); err != nil {
			logger.ErrorAttrs("http_request_failed", slog.String("provider", meta.Provider), slog.String("operation", meta.Operation),
				slog.String("method", req.Method), slog.String("url", safeURL(req.URL)), slog.Int("status", resp.StatusCode),
				slog.Int("response_bytes", len(body)), slog.Int64("duration_ms", time.Since(started).Milliseconds()), slog.String("outcome", "decode_error"))
			return nil, err
		}
	}
	logger.InfoAttrs("http_request_completed", slog.String("provider", meta.Provider), slog.String("operation", meta.Operation),
		slog.String("method", req.Method), slog.String("url", safeURL(req.URL)), slog.Int("status", resp.StatusCode),
		slog.Int("request_bytes", requestBytes(req)), slog.Int("response_bytes", len(body)),
		slog.Int64("duration_ms", time.Since(started).Milliseconds()), slog.String("outcome", "success"))
	return body, nil
}

func classifyHTTPError(ctx context.Context, err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	return "transport_error"
}

func requestBytes(req *http.Request) int {
	if req == nil || req.Body == nil {
		return 0
	}
	if req.ContentLength >= 0 {
		return int(req.ContentLength)
	}
	return -1
}

func safeURL(value *url.URL) string {
	if value == nil {
		return ""
	}
	return value.Scheme + "://" + value.Host + value.Path
}
