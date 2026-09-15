package proxy

import (
	"net/http"
	"net/url"
)

// Client clones the client and transport without changing the shared originals.
// A nil endpoint leaves the caller's configured transport untouched.
func Client(base *http.Client, endpoint *Endpoint) *http.Client {
	if base == nil {
		base = http.DefaultClient
	}
	if endpoint == nil {
		return base
	}
	copy := *base
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if existing, ok := base.Transport.(*http.Transport); ok {
		transport = existing.Clone()
	}
	u, _ := url.Parse(endpoint.address)
	transport.Proxy = http.ProxyURL(u)
	copy.Transport = transport
	return &copy
}
