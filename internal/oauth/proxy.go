package oauth

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

type httpProxyContextKey struct{}

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

func HTTPProxyFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(httpProxyContextKey{}).(string)
	return strings.TrimSpace(value)
}

func withSessionProxy(ctx context.Context, proxy string) context.Context {
	if strings.TrimSpace(proxy) == "" {
		return ctx
	}
	return WithHTTPProxy(ctx, proxy)
}

func ParseHTTPProxy(proxy string) (*url.URL, error) {
	proxy = strings.TrimSpace(proxy)
	if proxy == "" {
		return nil, nil
	}
	proxyURL, err := url.Parse(proxy)
	if err != nil || proxyURL.Scheme == "" || proxyURL.Host == "" {
		return nil, errors.New("invalid proxy")
	}
	switch strings.ToLower(proxyURL.Scheme) {
	case "http", "https", "socks5", "socks5h":
		return proxyURL, nil
	default:
		return nil, errors.New("invalid proxy")
	}
}
