package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sync"

	"ai-unisub2/internal/oauth"
)

type oauthProvider struct {
	mu      sync.RWMutex
	config  ProviderConfig
	manager *oauth.OAuthManager
	client  *http.Client
}

func newOAuthProvider(id string, raw json.RawMessage, manager *oauth.OAuthManager) (oauthProvider, error) {
	if id == "" {
		return oauthProvider{}, errors.New("provider instance ID is empty")
	}
	if manager == nil {
		return oauthProvider{}, errors.New("oauth manager is required")
	}
	var config ProviderConfig
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &config); err != nil {
			return oauthProvider{}, err
		}
	}
	config.ID, config.Enabled = id, true
	client := &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}
	if config.Proxy != "" {
		proxyURL, err := url.Parse(config.Proxy)
		if err != nil || proxyURL.Scheme == "" || proxyURL.Host == "" {
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
	var next ProviderConfig
	if err := json.Unmarshal(raw, &next); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if next.ID != "" && next.ID != p.config.ID {
		return errors.New("provider config ID cannot be changed")
	}
	next.ID, next.Enabled = p.config.ID, true
	if next.Proxy != p.config.Proxy {
		client := &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}
		if next.Proxy != "" {
			proxyURL, err := url.Parse(next.Proxy)
			if err != nil || proxyURL.Scheme == "" || proxyURL.Host == "" {
				return errors.New("invalid provider proxy")
			}
			client.Transport.(*http.Transport).Proxy = http.ProxyURL(proxyURL)
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
	token, err := p.manager.GetValidAccessToken(req.Context(), credentialID)
	if err != nil {
		trace.HTTPErrorCode, trace.HTTPErrorInfo = http.StatusUnauthorized, "unable to obtain provider access token"
		if recorder != nil {
			recorder(trace)
		}
		return
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	response, err := client.Do(req)
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
