package aiprovider

import (
	"ai-unisub/internal/common"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"ai-unisub/internal/oauth"
)

type oauthAIProvider struct {
	mu       sync.RWMutex
	config   AIProviderConfig
	manager  *oauth.OAuthManager
	client   *http.Client
	resolver ProxyResolver
}

func (p *oauthAIProvider) SetProxyResolver(r ProxyResolver) {
	p.mu.Lock()
	p.resolver = r
	p.mu.Unlock()
}

func newOAuthAIProvider(id string, raw json.RawMessage, manager *oauth.OAuthManager) (*oauthAIProvider, error) {
	if id == "" {
		return nil, errors.New("provider instance ID is empty")
	}
	if manager == nil {
		return nil, errors.New("oauth manager is required")
	}
	config, err := decodeAIProviderConfig(id, raw)
	if err != nil {
		return nil, err
	}
	client := upstreamClient(http.DefaultTransport.(*http.Transport).Clone())
	if config.Proxy != "" {
		proxyURL, err := url.Parse(config.Proxy)
		if err != nil {
			return nil, errors.New("invalid provider proxy")
		}
		client.Transport.(*http.Transport).Proxy = http.ProxyURL(proxyURL)
	}
	return &oauthAIProvider{config: cloneAIProviderConfig(config), manager: manager, client: client}, nil
}

func (p *oauthAIProvider) Config() AIProviderConfig {
	if p == nil {
		return AIProviderConfig{}
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return cloneAIProviderConfig(p.config)
}

func (p *oauthAIProvider) update(raw json.RawMessage) error {
	if p == nil {
		return errors.New("provider is nil")
	}
	var probe struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return err
	}
	current := p.Config()
	if probe.ID != "" && probe.ID != current.ID {
		return errors.New("provider config ID cannot be changed")
	}
	next, err := decodeAIProviderConfig(current.ID, raw)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if next.ProxyGroupID != p.config.ProxyGroupID {
		client := upstreamClient(http.DefaultTransport.(*http.Transport).Clone())
		p.client = client
	}
	if next.Proxy != p.config.Proxy && next.ProxyGroupID == "" {
		client := upstreamClient(http.DefaultTransport.(*http.Transport).Clone())
		if next.Proxy != "" {
			u, e := url.Parse(next.Proxy)
			if e != nil {
				return e
			}
			client.Transport.(*http.Transport).Proxy = http.ProxyURL(u)
		}
		p.client = client
	}
	p.config = cloneAIProviderConfig(next)
	return nil
}

func (p *oauthAIProvider) handle(service, credentialID string, req *http.Request, recorder APICallRecorder) {
	trace := &AIProviderCallTrace{}
	if p == nil || req == nil || req.URL == nil {
		trace.HTTPErrorCode = http.StatusBadRequest
		trace.HTTPErrorInfo = "invalid provider request"
		if recorder != nil {
			recorder(trace)
		}
		return
	}
	trace.URL, trace.OriginalRequestHeaders = req.URL.String(), req.Header.Clone()
	req = req.Clone(req.Context())
	req.RequestURI = ""
	var body []byte
	var err error
	if req.Body != nil {
		body, err = io.ReadAll(req.Body)
		_ = req.Body.Close()
	}
	trace.RequestBody = append([]byte(nil), body...)
	if err != nil {
		trace.HTTPErrorCode, trace.HTTPErrorInfo = http.StatusBadRequest, err.Error()
		if recorder != nil {
			recorder(trace)
		}
		return
	}
	p.mu.RLock()
	groupID, resolver := p.config.ProxyGroupID, p.resolver
	p.mu.RUnlock()
	proxyURL := ""
	if resolver != nil && groupID != "" {
		proxyURL, err = resolver.ResolveProxy(req.Context(), groupID)
		if err != nil {
			trace.HTTPErrorInfo = err.Error()
			if recorder != nil {
				recorder(trace)
			}
			return
		}
	}
	config := p.Config()
	token := config.APIKey
	if config.AuthType != AuthTypeAPIKey {
		token, err = p.manager.GetValidAccessToken(common.WithHTTPProxy(req.Context(), proxyURL), service, credentialID)
	}
	if err != nil {
		trace.HTTPErrorCode, trace.HTTPErrorInfo = http.StatusUnauthorized, "unable to obtain provider access token"
		if recorder != nil {
			recorder(trace)
		}
		return
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.Header.Del("Cookie")
	req.Header.Del("X-Api-Key")
	if service == oauth.OAuthServiceClaude && config.AuthType == AuthTypeAPIKey {
		req.Header.Del("Authorization")
		req.Header.Set("X-Api-Key", token)
		if req.Header.Get("Anthropic-Version") == "" {
			req.Header.Set("Anthropic-Version", "2023-06-01")
		}
	} else {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	var client *http.Client
	if proxyURL != "" {
		parsed, parseErr := url.Parse(proxyURL)
		if parseErr != nil {
			trace.HTTPErrorInfo = parseErr.Error()
			if recorder != nil {
				recorder(trace)
			}
			return
		}
		req = req.Clone(req.Context())
		p.mu.RLock()
		client = p.client
		p.mu.RUnlock()
		transport := client.Transport.(*http.Transport).Clone()
		transport.Proxy = http.ProxyURL(parsed)
		client = upstreamClient(transport)
	}
	p.mu.RLock()
	if client == nil {
		client = p.client
	}
	p.mu.RUnlock()
	maxRetries := 0
	if resolver != nil && groupID != "" {
		maxRetries = resolver.ProxyRetryLimit(groupID)
	}
	var response *http.Response
	for attempt := 0; ; attempt++ {
		req.Body = io.NopCloser(bytes.NewReader(body))
		response, err = client.Do(req)
		retryable := err != nil || (response != nil && response.StatusCode >= 500)
		if resolver != nil && groupID != "" {
			resolver.ReportProxy(groupID, proxyURL, !retryable)
		}
		if !retryable || attempt >= maxRetries || resolver == nil || groupID == "" {
			break
		}
		if response != nil {
			response.Body.Close()
		}
		proxyURL, err = resolver.ResolveProxy(req.Context(), groupID)
		if err != nil {
			break
		}
		parsed, parseErr := url.Parse(proxyURL)
		if parseErr != nil {
			err = parseErr
			break
		}
		p.mu.RLock()
		baseClient := p.client
		p.mu.RUnlock()
		transport := baseClient.Transport.(*http.Transport).Clone()
		transport.Proxy = http.ProxyURL(parsed)
		client = upstreamClient(transport)
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	trace.OutboundRequestHeaders = req.Header.Clone()
	if err != nil {
		trace.HTTPErrorInfo = err.Error()
		if recorder != nil {
			recorder(trace)
		}
		return
	}
	defer response.Body.Close()
	trace.ResponseHeaders = response.Header.Clone()
	trace.ResponseStatus = response.StatusCode
	if w := responseWriter(req.Context()); w != nil {
		for key, values := range response.Header {
			if hopHeader(key) || strings.EqualFold(key, "Set-Cookie") {
				continue
			}
			w.Header()[key] = append([]string(nil), values...)
		}
		w.WriteHeader(response.StatusCode)
		capture := streamCapture{trace: trace, sse: strings.Contains(response.Header.Get("Content-Type"), "text/event-stream")}
		_, err = io.Copy(flushWriter{w}, io.TeeReader(response.Body, &capture))
		trace.ResponseBody = capture.Bytes()
	} else {
		trace.ResponseBody, err = io.ReadAll(response.Body)
	}
	if err != nil {
		trace.HTTPErrorInfo = err.Error()
	}
	if response.StatusCode >= 400 {
		trace.HTTPErrorCode = response.StatusCode
	}
	parseUsage(trace)
	if recorder != nil {
		recorder(trace)
	}
}

// Capture at most 1 MiB for inspection without buffering a streaming response.
type limitedCapture struct{ bytes.Buffer }

func (b *limitedCapture) Write(p []byte) (int, error) {
	n := len(p)
	if left := (1 << 20) - b.Len(); left > 0 {
		_, _ = b.Buffer.Write(p[:min(left, n)])
	}
	return n, nil
}

type flushWriter struct{ http.ResponseWriter }

func (w flushWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
	return n, err
}
func hopHeader(key string) bool {
	switch strings.ToLower(key) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	}
	return false
}
func (p *oauthAIProvider) usage(context.Context) ([]UsageItem, error) { return []UsageItem{}, nil }
func (p *oauthAIProvider) reset(context.Context) error                { return nil }

func cloneAIProviderConfig(config AIProviderConfig) AIProviderConfig {
	config.Labels = append([]string(nil), config.Labels...)
	return config
}

func upstreamClient(transport http.RoundTripper) *http.Client {
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
