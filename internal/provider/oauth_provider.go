package provider

import (
	"ai-unisub/internal/common"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sync"

	"ai-unisub/internal/oauth"
)

type oauthProvider struct {
	mu       sync.RWMutex
	config   ProviderConfig
	manager  *oauth.OAuthManager
	client   *http.Client
	resolver ProxyResolver
}

func (p *oauthProvider) SetProxyResolver(r ProxyResolver) { p.mu.Lock(); p.resolver = r; p.mu.Unlock() }

func newOAuthProvider(id string, raw json.RawMessage, manager *oauth.OAuthManager) (oauthProvider, error) {
	if id == "" {
		return oauthProvider{}, errors.New("provider instance ID is empty")
	}
	if manager == nil {
		return oauthProvider{}, errors.New("oauth manager is required")
	}
	config, err := decodeProviderConfig(id, raw)
	if err != nil {
		return oauthProvider{}, err
	}
	client := &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}
	if config.Proxy != "" {
		proxyURL, err := url.Parse(config.Proxy)
		if err != nil {
			return oauthProvider{}, errors.New("invalid provider proxy")
		}
		client.Transport.(*http.Transport).Proxy = http.ProxyURL(proxyURL)
	}
	return oauthProvider{config: cloneProviderConfig(config), manager: manager, client: client}, nil
}

func (p *oauthProvider) Config() ProviderConfig {
	if p == nil {
		return ProviderConfig{}
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return cloneProviderConfig(p.config)
}

func (p *oauthProvider) update(raw json.RawMessage) error {
	if p == nil {
		return errors.New("provider is nil")
	}
	var probe struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return err
	}
	if probe.ID != "" && probe.ID != p.config.ID {
		return errors.New("provider config ID cannot be changed")
	}
	next, err := decodeProviderConfig(p.config.ID, raw)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if next.ProxyGroupID != p.config.ProxyGroupID {
		client := &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}
		p.client = client
	}
	if next.Proxy != p.config.Proxy && next.ProxyGroupID == "" {
		client := &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}
		if next.Proxy != "" {
			u, e := url.Parse(next.Proxy)
			if e != nil {
				return e
			}
			client.Transport.(*http.Transport).Proxy = http.ProxyURL(u)
		}
		p.client = client
	}
	p.config = cloneProviderConfig(next)
	return nil
}

func (p *oauthProvider) handle(service, credentialID string, req *http.Request, recorder APICallRecorder) {
	trace := &ProviderCallTrace{}
	if p == nil || req == nil || req.URL == nil {
		trace.HTTPErrorCode = http.StatusBadRequest
		trace.HTTPErrorInfo = "invalid provider request"
		if recorder != nil {
			recorder(trace)
		}
		return
	}
	trace.URL, trace.OriginalRequestHeaders = req.URL.String(), req.Header.Clone()
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
	token, err := p.manager.GetValidAccessToken(common.WithHTTPProxy(req.Context(), proxyURL), service, credentialID)
	if err != nil {
		trace.HTTPErrorCode, trace.HTTPErrorInfo = http.StatusUnauthorized, "unable to obtain provider access token"
		if recorder != nil {
			recorder(trace)
		}
		return
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
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
		client = &http.Client{Transport: transport}
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
		client = &http.Client{Transport: transport}
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
	trace.ResponseBody, err = io.ReadAll(response.Body)
	if err != nil {
		trace.HTTPErrorInfo = err.Error()
	}
	if response.StatusCode >= 400 {
		trace.HTTPErrorCode = response.StatusCode
	}
	_ = service
	if recorder != nil {
		recorder(trace)
	}
}

func (p *oauthProvider) usage(context.Context) ([]UsageItem, error) { return []UsageItem{}, nil }
func (p *oauthProvider) reset(context.Context) error                { return nil }

func cloneProviderConfig(config ProviderConfig) ProviderConfig {
	config.Labels = append([]string(nil), config.Labels...)
	return config
}
