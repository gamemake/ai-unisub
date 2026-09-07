package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
	xproxy "golang.org/x/net/proxy"
)

const (
	socks5DialTimeout   = 10 * time.Second
	socks5DialKeepAlive = 30 * time.Second
)

func normalizeProxyURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", errors.New("proxy_url must be a valid http:// or socks5:// URL")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	switch parsed.Scheme {
	case "http", "socks5", "socks5h":
	default:
		return "", errors.New("proxy_url scheme must be http, socks5, or socks5h")
	}
	if parsed.Hostname() == "" || parsed.Port() == "" {
		return "", errors.New("proxy_url must include a host and port")
	}
	if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("proxy_url must not include a path, query, or fragment")
	}
	// Prefer socks5h semantics (remote DNS) even when callers write socks5://.
	if parsed.Scheme == "socks5" {
		parsed.Scheme = "socks5h"
	}
	return parsed.String(), nil
}

func (s *Server) clientForSubscription(account model.Subscription) (*http.Client, error) {
	if value, ok := s.subscriptionClients.Load(account.ID); ok {
		return value.(*http.Client), nil
	}
	proxyURL := ""
	if account.ProxyConfigured {
		raw, err := s.repo.ProxyURL(account)
		if err != nil {
			return nil, errors.New("could not read account proxy")
		}
		proxyURL = raw
	}
	client, err := newClientForProxy(proxyURL)
	if err != nil {
		return nil, err
	}
	value, loaded := s.subscriptionClients.LoadOrStore(account.ID, client)
	if loaded {
		if transport, ok := client.Transport.(*http.Transport); ok {
			transport.CloseIdleConnections()
		}
		return value.(*http.Client), nil
	}
	return client, nil
}

func newClientForProxy(raw string) (*http.Client, error) {
	transport := newHTTPTransport()
	if raw != "" {
		proxyURL, err := url.Parse(raw)
		if err != nil {
			return nil, errors.New("stored account proxy is invalid")
		}
		switch strings.ToLower(proxyURL.Scheme) {
		case "http":
			transport.Proxy = http.ProxyURL(proxyURL)
		case "socks5", "socks5h":
			socksURL := *proxyURL
			socksURL.Scheme = "socks5h"
			forward := &net.Dialer{Timeout: socks5DialTimeout, KeepAlive: socks5DialKeepAlive}
			dialer, dialErr := xproxy.FromURL(&socksURL, forward)
			if dialErr != nil {
				return nil, errors.New("could not configure SOCKS5 proxy")
			}
			transport.Proxy = nil
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				if contextDialer, ok := dialer.(xproxy.ContextDialer); ok {
					return contextDialer.DialContext(ctx, network, address)
				}
				return dialer.Dial(network, address)
			}
		default:
			return nil, errors.New("stored account proxy scheme is unsupported")
		}
	}
	return &http.Client{
		Transport:     transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

func (s *Server) closeSubscriptionClient(accountID int64) {
	value, ok := s.subscriptionClients.LoadAndDelete(accountID)
	if !ok {
		return
	}
	if transport, ok := value.(*http.Client).Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
}
