// Package common contains cross-cutting primitives shared by internal modules.
package common

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

type httpProxyContextKey struct{}

// WithHTTPProxy returns a context carrying the normalized HTTP proxy URL.
// An empty proxy leaves the context unchanged.
func WithHTTPProxy(ctx context.Context, proxy string) context.Context {
	proxy = strings.TrimSpace(proxy)
	if ctx == nil {
		ctx = context.Background()
	}
	if proxy == "" {
		return ctx
	}
	return context.WithValue(ctx, httpProxyContextKey{}, proxy)
}

// HTTPProxyFrom returns the proxy URL carried by ctx, or an empty string.
func HTTPProxyFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(httpProxyContextKey{}).(string)
	return strings.TrimSpace(value)
}

// ParseHTTPProxy validates a supported HTTP proxy URL. An empty proxy is
// valid and returns (nil, nil).
func ParseHTTPProxy(proxy string) (*url.URL, error) {
	proxy = strings.TrimSpace(proxy)
	if proxy == "" {
		return nil, nil
	}
	proxyURL, err := url.Parse(proxy)
	if err != nil || proxyURL.Scheme == "" || proxyURL.Host == "" {
		return nil, errors.New(MessageInvalidProxy)
	}
	switch strings.ToLower(proxyURL.Scheme) {
	case "http", "https", "socks5", "socks5h":
		return proxyURL, nil
	default:
		return nil, errors.New(MessageInvalidProxy)
	}
}
