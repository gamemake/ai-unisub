package aiprovider

import (
	"ai-unisub/internal/oauth"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrQuotaUnsupported     = errors.New("supplier does not support this quota query")
	ErrQuotaNotConfigured   = errors.New("quota query credentials or configuration are incomplete")
	ErrQuotaInvalidResponse = errors.New("invalid quota response")
	ErrQuotaAuthentication  = errors.New("quota query authentication failed")
	ErrQuotaRateLimited     = errors.New("quota query rate limited")
	ErrQuotaUpstream        = errors.New("quota query upstream unavailable")
	ErrQuotaSuperseded      = errors.New("quota query superseded by a newer configuration or query")
)

func (m *AIProviderManager) quotaSupplier(id string) Supplier {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.catalog.Suppliers {
		if s.ID == id {
			return cloneCatalog(Catalog{Suppliers: []Supplier{s}}).Suppliers[0]
		}
	}
	return Supplier{}
}

func (p *oauthAIProvider) setQuotaSupplier(source func(string) Supplier) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.quotaSupplier = source
}

func (p *oauthAIProvider) invalidateQuotaCache() { p.quotaCache.invalidate() }

func quotaEndpoint(c AIProviderConfig) (endpoint, service string, err error) {
	if c.Kind == "api" || c.Kind == "" && c.AuthType == AuthTypeAPIKey {
		switch c.Supplier {
		case "deepseek":
			endpoint = "https://api.deepseek.com/user/balance"
		case "kimi":
			endpoint = "https://api.moonshot.cn/v1/users/me/balance"
		default:
			return "", "", ErrQuotaUnsupported
		}
	} else {
		switch c.Supplier {
		case "anthropic":
			endpoint, service = "https://api.anthropic.com/api/oauth/usage", oauth.OAuthServiceClaude
		case "openai":
			endpoint, service = "https://chatgpt.com/backend-api/wham/usage", oauth.OAuthServiceCodex
		case "grok":
			endpoint, service = "https://cli-chat-proxy.grok.com/v1/billing?format=credits", oauth.OAuthServiceGrok
		default:
			return "", "", ErrQuotaUnsupported
		}
	}
	// Never send a custom upstream's credentials to the original supplier.
	if c.APIEndpoint != "" {
		configured, parseErr := url.Parse(c.APIEndpoint)
		fixed, _ := url.Parse(endpoint)
		if parseErr != nil || configured.User != nil || configured.Scheme != fixed.Scheme || !strings.EqualFold(configured.Host, fixed.Host) {
			return "", "", ErrQuotaNotConfigured
		}
	}
	return endpoint, service, nil
}

func validQuotaHeaders(headers map[string]string) bool {
	seen := map[string]bool{}
	for name, value := range headers {
		key := strings.ToLower(name)
		if name == "" || seen[key] || strings.ContainsAny(value, "\r\n\x00") || hopHeader(key) {
			return false
		}
		for _, ch := range name {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", ch)) {
				return false
			}
		}
		switch key {
		case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key", "api-key", "host", "content-length", "chatgpt-account-id", "openai-organization", "openai-project":
			return false
		}
		seen[key] = true
	}
	return true
}

func (p *oauthAIProvider) quota(ctx context.Context, fallback string) (*Quota, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.RLock()
	config, baseClient, resolver, source := cloneAIProviderConfig(p.config), p.client, p.resolver, p.quotaSupplier
	stamp := p.quotaCache.begin()
	p.mu.RUnlock()
	if config.Supplier == "" {
		config.Supplier = fallback
	}
	endpointURL, service, err := quotaEndpoint(config)
	if err != nil {
		return nil, err
	}
	if service == "" && config.APIKey == "" || service != "" && (config.CredentialID == "" || p.manager == nil) {
		return nil, ErrQuotaNotConfigured
	}
	var supplier Supplier
	if source != nil {
		supplier = source(config.Supplier)
	}
	overrides := supplier.APIUsageHeaderOverrides
	if service != "" {
		overrides = supplier.SubscriptionUsageHeaderOverrides
	}
	if !validQuotaHeaders(overrides) {
		return nil, ErrQuotaNotConfigured
	}
	if config.ProxyGroupID != 0 {
		if resolver == nil {
			return nil, ErrQuotaNotConfigured
		}
		var selected *http.Client
		if !p.withProxy(ctx, resolver, config.ProxyGroupID, config.Supplier, func(c *http.Client) { selected = c }) || selected == nil {
			return nil, ErrQuotaUpstream
		}
		baseClient = selected
	}
	if baseClient == nil {
		baseClient = upstreamClient(http.DefaultTransport.(*http.Transport).Clone())
	}
	client := *baseClient
	client.Timeout = 25 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	token, accountID := config.APIKey, ""
	if service != "" {
		token, err = p.manager.GetValidAccessToken(ctx, service, config.CredentialID, &client)
		if err != nil {
			return nil, ErrQuotaAuthentication
		}
		if service == oauth.OAuthServiceCodex {
			accountID, err = p.manager.CredentialAccountID(config.CredentialID)
			if err != nil || accountID == "" {
				return nil, ErrQuotaNotConfigured
			}
		}
	}
	// Reuse authentication and proxy selection for all parts of one snapshot.
	query := func(endpointURL string) ([]QuotaItem, error) {
		for attempt := 0; attempt < 2; attempt++ {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpointURL, nil)
			if err != nil {
				return nil, ErrQuotaNotConfigured
			}
			for k, v := range defaultQuotaRequestHeaders {
				req.Header.Set(k, v)
			}
			if service == oauth.OAuthServiceClaude {
				req.Header.Set("anthropic-beta", "oauth-2025-04-20")
				req.Header.Set("Accept", "application/json, text/plain, */*")
				req.Header.Set("User-Agent", "claude-code/2.1.7")
			}
			if service == oauth.OAuthServiceCodex {
				req.Header.Set("User-Agent", "codex-cli")
				req.Header.Set("OpenAI-Beta", "codex-1")
				req.Header.Set("originator", "Codex Desktop")
				req.Header.Set("oai-language", "zh-CN")
				req.Header.Set("sec-fetch-site", "none")
				req.Header.Set("sec-fetch-mode", "no-cors")
				req.Header.Set("sec-fetch-dest", "empty")
				req.Header.Set("priority", "u=4, i")
			}
			if service == oauth.OAuthServiceGrok {
				req.Header.Set("x-xai-token-auth", "xai-grok-cli")
				req.Header.Set("x-grok-client-version", "0.2.114")
				req.Header.Set("User-Agent", "grok-pager/0.2.114 grok-shell/0.2.114 (macos; aarch64)")
			}
			for k, v := range overrides {
				req.Header.Set(k, v)
			}
			req.Header.Set("Authorization", "Bearer "+token)
			if accountID != "" {
				req.Header.Set("ChatGPT-Account-Id", accountID)
			}
			resp, err := client.Do(req)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, ErrQuotaUpstream
			}
			status := resp.StatusCode
			if status == 401 && service != "" && attempt == 0 {
				resp.Body.Close()
				token, err = p.manager.RecoverAccessToken(ctx, service, config.CredentialID, token, &client)
				if err != nil {
					return nil, ErrQuotaAuthentication
				}
				continue
			}
			if status != http.StatusOK {
				resp.Body.Close()
				switch status {
				case 401, 403:
					return nil, ErrQuotaAuthentication
				case 429:
					return nil, ErrQuotaRateLimited
				default:
					return nil, ErrQuotaUpstream
				}
			}
			body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
			resp.Body.Close()
			if err != nil || len(body) > 1<<20 {
				return nil, ErrQuotaInvalidResponse
			}
			if service == oauth.OAuthServiceGrok {
				if err := validateGrokBillingWindow(body, !strings.HasSuffix(endpointURL, "?format=credits")); err != nil {
					return nil, err
				}
			}
			items, err := parseSupplierQuota(config.Supplier, body)
			if err != nil {
				return nil, err
			}
			return items, nil
		}
		return nil, ErrQuotaAuthentication
	}
	items, err := query(endpointURL)
	if err != nil {
		return nil, err
	}
	if service == oauth.OAuthServiceGrok {
		for i := range items {
			items[i].Source = "billing?format=credits"
		}
		monthly, err := query(strings.TrimSuffix(endpointURL, "?format=credits"))
		if err != nil {
			return nil, err
		}
		for i := range monthly {
			monthly[i].Source = "billing"
		}
		items = append(items, monthly...)
	}
	// Commit the complete snapshot atomically. A failed window leaves the old
	// snapshot and its observation times untouched, including on first fetch.
	if len(items) > 128 {
		return nil, ErrQuotaInvalidResponse
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stamp.observed = time.Now().UTC()
	if service != "" {
		updates, err := subscriptionBodyUpdates(config.Supplier, items, stamp.observed)
		if err != nil {
			return nil, err
		}
		if !p.quotaCache.putSubscription(stamp, updates, false) {
			return nil, ErrQuotaSuperseded
		}
		result := &Quota{CacheStatus: QuotaCacheFresh, UpdatedAt: stamp.observed}
		for _, update := range updates {
			result.Subscription = append(result.Subscription, update.item)
		}
		return result, nil
	}
	if !p.quotaCache.put(stamp, items, false) {
		return nil, ErrQuotaSuperseded
	}
	return &Quota{Items: items, CacheStatus: QuotaCacheFresh, UpdatedAt: stamp.observed}, nil
}
