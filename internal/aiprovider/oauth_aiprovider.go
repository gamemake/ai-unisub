package aiprovider

import (
	"ai-unisub/internal/proxy"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"ai-unisub/internal/oauth"
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
	p.config = cloneAIProviderConfig(next)
	p.quotaCache.invalidate()
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
	var endpoint *proxy.Endpoint
	tried := []string{}
	if resolver != nil && groupID != "" {
		endpoint, err = resolver.ResolveProxy(req.Context(), groupID, application, tried)
		if err != nil {
			trace.HTTPErrorInfo = err.Error()
			trace.RetrySafe = true
			if recorder != nil {
				recorder(trace)
			}
			return
		}
	}
	if groupID != "" && resolver == nil {
		trace.HTTPErrorInfo = "proxy resolver is unavailable"
		proxy.LogError("resolve", nil, application, errors.New(trace.HTTPErrorInfo))
		if recorder != nil {
			recorder(trace)
		}
		return
	}
	token := config.APIKey
	if config.AuthType != AuthTypeAPIKey {
		token, err = p.manager.GetValidAccessToken(req.Context(), service, credentialID, endpoint)
	}
	if err != nil {
		if resolver != nil && groupID != "" {
			_ = proxy.ReportResult(resolver, endpoint, application, proxy.Canceled, err, 0)
		}
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
	p.mu.RLock()
	baseClient := p.client
	p.mu.RUnlock()
	client := proxy.Client(baseClient, endpoint)
	defer func() {
		if client != baseClient {
			client.CloseIdleConnections()
		}
	}()
	maxRetries := 0
	if resolver != nil && groupID != "" {
		maxRetries = resolver.ProxyRetryLimit(groupID)
	}
	var response *http.Response
	authRecovered := false
	for attempt := 0; ; attempt++ {
		req.Body = io.NopCloser(bytes.NewReader(body))
		response, err = client.Do(req)
		if err == nil && response.StatusCode == http.StatusUnauthorized && config.AuthType == AuthTypeOAuth && !authRecovered {
			authRecovered = true
			refreshed, refreshErr := p.manager.RecoverAccessToken(req.Context(), service, credentialID, token, endpoint)
			if refreshErr == nil {
				if resolver != nil && groupID != "" {
					if reportErr := proxy.ReportResult(resolver, endpoint, application, proxy.ApplicationIgnored, nil, response.StatusCode); reportErr != nil {
						response.Body.Close()
						err = reportErr
						break
					}
					var selectErr error
					endpoint, selectErr = resolver.ResolveProxy(req.Context(), groupID, application, nil)
					if selectErr != nil {
						response.Body.Close()
						err = selectErr
						break
					}
					if client != baseClient {
						client.CloseIdleConnections()
					}
					client = proxy.Client(baseClient, endpoint)
				}
				response.Body.Close()
				token = refreshed
				req.Header.Set("Authorization", "Bearer "+token)
				req.Body = io.NopCloser(bytes.NewReader(body))
				response, err = client.Do(req)
			}
		}
		dialError, isDialError := errors.AsType[*net.OpError](err)
		safe := isDialError && dialError.Op == "dial"
		trace.RetrySafe = safe
		retryable := safe || ((req.Method == http.MethodGet || req.Method == http.MethodHead) && (err != nil || (response != nil && response.StatusCode >= 500)))
		if resolver != nil && groupID != "" {
			class := proxy.Success
			if err != nil {
				class = proxy.NetworkError
			} else if response.StatusCode >= 500 {
				class = proxy.ApplicationError
			} else if response.StatusCode >= 400 {
				class = proxy.ApplicationIgnored
			}
			if err == nil && len(config.ProxyApplicationErrorStatuses) > 0 {
				class = proxy.Success
				if response.StatusCode >= 400 {
					class = proxy.ApplicationIgnored
				}
				if slices.Contains(config.ProxyApplicationErrorStatuses, response.StatusCode) {
					class = proxy.ApplicationError
				}
			}
			if req.Context().Err() != nil {
				class = proxy.Canceled
			}
			status := 0
			if response != nil {
				status = response.StatusCode
			}
			if reportErr := proxy.ReportResult(resolver, endpoint, application, class, err, status); reportErr != nil {
				maxRetries = 0
			}
			tried = append(tried, endpoint.String())
		}
		if !retryable || attempt >= maxRetries || resolver == nil || groupID == "" {
			break
		}
		if response != nil {
			response.Body.Close()
		}
		endpoint, err = resolver.ResolveProxy(req.Context(), groupID, application, tried)
		if err != nil {
			break
		}
		if client != baseClient {
			client.CloseIdleConnections()
		}
		client = proxy.Client(baseClient, endpoint)
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
		p.quotaCache.putSubscription(quotaStamp, subscriptionHeaderUpdates(config, service, response.Header, quotaStamp.observed), true)
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
		if endpoint != nil {
			proxy.LogError("read_response", endpoint, application, err)
		}
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
