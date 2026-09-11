package oauth

import (
	"ai-unisub/internal/common"
	"context"
	"net/url"
)

// These forwarding functions preserve the OAuth package API while the shared
// implementation lives in internal/common.
func WithHTTPProxy(ctx context.Context, proxy string) context.Context {
	return common.WithHTTPProxy(ctx, proxy)
}

func HTTPProxyFrom(ctx context.Context) string {
	return common.HTTPProxyFrom(ctx)
}

func withSessionProxy(ctx context.Context, proxy string) context.Context {
	if proxy == "" {
		return ctx
	}
	return WithHTTPProxy(ctx, proxy)
}

func ParseHTTPProxy(proxy string) (*url.URL, error) {
	return common.ParseHTTPProxy(proxy)
}
