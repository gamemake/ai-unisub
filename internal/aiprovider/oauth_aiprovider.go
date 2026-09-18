package aiprovider

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"ai-unisub/internal/oauth"
	"ai-unisub/internal/proxy"
)

type oauthAIProvider struct {
	mu            sync.RWMutex
	config        AIProviderConfig
	manager       *oauth.OAuthManager
	client        *http.Client
	resolver      ProxyResolver
	quotaCache    quotaCache
	quotaSupplier func(string) Supplier
}

func (p *oauthAIProvider) SetProxyResolver(r ProxyResolver) {
	p.mu.Lock()
	p.resolver = r
	p.mu.Unlock()
}

func newOAuthAIProvider(id int, data ProviderData, manager *oauth.OAuthManager) (*oauthAIProvider, error) {
	if id == 0 {
		return nil, errors.New("provider instance ID is empty")
	}
	if manager == nil {
		return nil, errors.New("oauth manager is required")
	}
	config, err := decodeAIProviderConfig(id, data.Config)
	if err != nil {
		return nil, err
	}
	client := upstreamClient(http.DefaultTransport.(*http.Transport).Clone())
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

func (p *oauthAIProvider) State() AIProviderState {
	if p == nil {
		return AIProviderState{}
	}
	return AIProviderState{Quota: p.quotaCache.snapshot()}
}

func (p *oauthAIProvider) Quota() AIProviderQuota {
	if p == nil {
		return AIProviderQuota{}
	}
	return p.quotaCache.snapshot()
}

func (p *oauthAIProvider) RestoreState(raw json.RawMessage) error {
	return restoreAIProviderState(&p.quotaCache, raw)
}

func (p *oauthAIProvider) update(raw json.RawMessage) error {
	if p == nil {
		return errors.New("provider is nil")
	}
	var probe struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return err
	}
	current := p.Config()
	if probe.ID != 0 && probe.ID != current.ID {
		return errors.New("provider config ID cannot be changed")
	}
	next, err := decodeAIProviderConfig(current.ID, raw)
	if err != nil {
		return err
	}
	p.mu.Lock()
	if next.ProxyGroupID != p.config.ProxyGroupID {
		client := upstreamClient(http.DefaultTransport.(*http.Transport).Clone())
		p.client = client
	}
	p.config = cloneAIProviderConfig(next)
	p.quotaCache.invalidate()
	p.mu.Unlock()
	return p.quotaCache.save()
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
	p.mu.RLock()
	config := cloneAIProviderConfig(p.config)
	quotaStamp := p.quotaCache.begin()
	p.mu.RUnlock()
	application := cmp.Or(config.Supplier, service)
	if config.AuthType == AuthTypeAPIKey && config.Supplier != "" {
		service = config.Supplier
		if service == "anthropic" {
			service = oauth.OAuthServiceClaude
		}
	}
	p.mu.RLock()
	baseClient := p.client
	p.mu.RUnlock()
	if baseClient == nil {
		baseClient = http.DefaultClient
	}
	token := config.APIKey
	if config.AuthType != AuthTypeAPIKey {
		called := p.withProxy(req.Context(), resolver, groupID, application, func(client *http.Client) {
			token, err = p.manager.GetValidAccessToken(req.Context(), service, credentialID, client)
		})
		if !called {
			err = errors.New("proxy group is unavailable")
		}
	}
	if err != nil {
		trace.HTTPErrorCode, trace.HTTPErrorInfo = http.StatusUnauthorized, "unable to obtain provider access token"
		trace.RetrySafe = true
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
	var response *http.Response
	authRecovered := false
	maxRetries := 0
	if resolver != nil && groupID != 0 {
		maxRetries = resolver.ProxyRetryLimit(groupID)
	}
	for attempt := 0; ; attempt++ {
		req.Body = io.NopCloser(bytes.NewReader(body))
		called := p.withProxy(req.Context(), resolver, groupID, application, func(client *http.Client) {
			response, err = client.Do(req)
		})
		if !called {
			err = errors.New("proxy group is unavailable")
		}
		if err == nil && response.StatusCode == http.StatusUnauthorized && config.AuthType == AuthTypeOAuth && !authRecovered {
			authRecovered = true
			var refreshed string
			var refreshErr error
			p.withProxy(req.Context(), resolver, groupID, application, func(client *http.Client) {
				refreshed, refreshErr = p.manager.RecoverAccessToken(req.Context(), service, credentialID, token, client)
			})
			if refreshErr == nil {
				response.Body.Close()
				token = refreshed
				req.Header.Set("Authorization", "Bearer "+token)
				req.Body = io.NopCloser(bytes.NewReader(body))
				p.withProxy(req.Context(), resolver, groupID, application, func(client *http.Client) { response, err = client.Do(req) })
			}
		}
		dialError, isDialError := errors.AsType[*net.OpError](err)
		safe := isDialError && dialError.Op == "dial"
		trace.RetrySafe = safe
		retryable := safe || (req.Method == http.MethodGet || req.Method == http.MethodHead) && (err != nil || response != nil && response.StatusCode >= 500)
		if !retryable || attempt >= maxRetries || groupID == 0 {
			break
		}
		if response != nil {
			response.Body.Close()
		}
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
	if response.StatusCode >= 200 && response.StatusCode < 300 || response.StatusCode == http.StatusTooManyRequests {
		quotaStamp.observed = time.Now().UTC()
		if p.quotaCache.putSubscription(quotaStamp, subscriptionHeaderUpdates(config, service, response.Header, quotaStamp.observed), true) {
			_ = p.quotaCache.save()
		}
	}
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

func (p *oauthAIProvider) withProxy(ctx context.Context, resolver ProxyResolver, groupID int, app string, fn func(*http.Client)) bool {
	if groupID == 0 {
		p.mu.RLock()
		client := p.client
		p.mu.RUnlock()
		if client == nil {
			client = http.DefaultClient
		}
		fn(client)
		return true
	}
	if resolver == nil {
		return false
	}
	ep, err := resolver.ResolveProxy(ctx, groupID, app, nil)
	if err != nil {
		return false
	}
	p.mu.RLock()
	base := p.client
	p.mu.RUnlock()
	fn(proxy.Client(base, ep))
	return true
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
func (p *oauthAIProvider) GetCachedQuota() *Quota {
	return p.quotaCache.get()
}
func (p *oauthAIProvider) reset(context.Context) error { return nil }

func cloneAIProviderConfig(config AIProviderConfig) AIProviderConfig {
	config.Labels = append([]string(nil), config.Labels...)
	config.Members = append([]GroupMember(nil), config.Members...)
	config.ProxyApplicationErrorStatuses = append([]int(nil), config.ProxyApplicationErrorStatuses...)
	return config
}

func upstreamClient(transport http.RoundTripper) *http.Client {
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
