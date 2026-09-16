// Package proxy owns outbound proxy configuration, scheduling and health.
package proxy

import (
	"ai-unisub/internal/common"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// Endpoint is immutable. Its URL, including authentication, never escapes by reference.
type Endpoint struct {
	address string
	lease   string
}

func (e *Endpoint) String() string {
	if e == nil {
		return ""
	}
	return e.address
}
func NewEndpoint(address string) (endpoint *Endpoint, err error) {
	defer func() { logOperationError("parse_endpoint", address, "", "", err) }()
	return newEndpoint(address)
}

func newEndpoint(address string) (*Endpoint, error) {
	address = strings.TrimSpace(address)
	invalid := errors.New(common.MessageInvalidProxy)
	if address == "" || strings.IndexFunc(address, unicode.IsSpace) >= 0 {
		return nil, invalid
	}
	u, err := url.Parse(address)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(address, "#") {
		return nil, invalid
	}
	u.Scheme = strings.ToLower(u.Scheme)
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, invalid
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if strings.HasSuffix(u.Host, ":") {
		return nil, invalid
	}
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, invalid
		}
		port = strconv.Itoa(n)
	}
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	} else {
		u.Host = host
	}
	if u.Path == "/" {
		u.Path = ""
		u.RawPath = ""
	}
	return &Endpoint{address: u.String()}, nil
}
