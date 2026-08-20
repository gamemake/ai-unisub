package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/ai-unisub/ai-unisub/internal/model"
	xproxy "golang.org/x/net/proxy"
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
	if parsed.Scheme != "http" && parsed.Scheme != "socks5" {
		return "", errors.New("proxy_url scheme must be http or socks5")
	}
	if parsed.Hostname() == "" || parsed.Port() == "" {
		return "", errors.New("proxy_url must include a host and port")
	}
	if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("proxy_url must not include a path, query, or fragment")
	}
	return parsed.String(), nil
}

func (s *Server) clientForAccount(account model.Account) (*http.Client, error) {
	if value, ok := s.accountClients.Load(account.ID); ok {
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
	value, loaded := s.accountClients.LoadOrStore(account.ID, client)
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
		switch proxyURL.Scheme {
		case "http":
			transport.Proxy = http.ProxyURL(proxyURL)
		case "socks5":
			var auth *xproxy.Auth
			if proxyURL.User != nil {
				password, _ := proxyURL.User.Password()
				auth = &xproxy.Auth{User: proxyURL.User.Username(), Password: password}
			}
			dialer, dialErr := xproxy.SOCKS5("tcp", proxyURL.Host, auth, xproxy.Direct)
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

func (s *Server) closeAccountClient(accountID int64) {
	value, ok := s.accountClients.LoadAndDelete(accountID)
	if !ok {
		return
	}
	if transport, ok := value.(*http.Client).Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
}
