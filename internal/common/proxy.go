// Package common contains cross-cutting primitives shared by internal modules.
package common

import (
	"context"
	"errors"
	"net/url"
	"strconv"
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
	if strings.ContainsAny(proxy, " \t\r\n") {
		return nil, errors.New(MessageInvalidProxy)
	}
	proxyURL, err := url.ParseRequestURI(proxy)
	if err != nil || proxyURL.Scheme == "" || proxyURL.Host == "" || proxyURL.Hostname() == "" || proxyURL.Opaque != "" || proxyURL.RawQuery != "" || proxyURL.Fragment != "" {
		return nil, errors.New(MessageInvalidProxy)
	}
	switch strings.ToLower(proxyURL.Scheme) {
	case "http", "https", "socks5", "socks5h":
		if port := proxyURL.Port(); port != "" {
			value, parseErr := strconv.Atoi(port)
			if parseErr != nil || value < 1 || value > 65535 {
				return nil, errors.New(MessageInvalidProxy)
			}
		}
		return proxyURL, nil
	default:
		return nil, errors.New(MessageInvalidProxy)
	}
}
