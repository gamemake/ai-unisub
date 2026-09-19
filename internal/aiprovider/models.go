package aiprovider

import (
	"ai-unisub/internal/oauth"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

var (
	// ErrModelsUnsupported means this provider kind cannot list models (e.g. groups).
	ErrModelsUnsupported = errors.New("provider does not support model listing")
	// ErrModelsNotConfigured means credentials or base URL are missing.
	ErrModelsNotConfigured = errors.New("model listing credentials or configuration are incomplete")
	// ErrModelsInvalidResponse means the upstream body could not be parsed.
	ErrModelsInvalidResponse = errors.New("invalid models response")
	// ErrModelsAuthentication means the upstream rejected credentials.
	ErrModelsAuthentication = errors.New("model listing authentication failed")
	// ErrModelsRateLimited means the upstream rate-limited the request.
	ErrModelsRateLimited = errors.New("model listing rate limited")
	// ErrModelsUpstream means a transport or non-auth upstream failure.
	ErrModelsUpstream = errors.New("model listing upstream unavailable")
)

// Codex OAuth tokens cannot call api.openai.com/v1/models. OpenAI subscription
// accounts load the public Codex models catalog instead. Uses the AIProvider
// proxy group when configured. github.com/.../raw/... redirects to raw.githubusercontent.com.
const codexModelsJSONURL = "https://github.com/openai/codex/raw/refs/heads/main/codex-rs/models-manager/models.json"

// ModelList is the public result of FetchModels.
type ModelList struct {
	Models []string `json:"models"`
}

// FetchModels queries the upstream model catalog for this provider instance.
// Groups return ErrModelsUnsupported. Results are not cached.
func (p *oauthAIProvider) FetchModels(ctx context.Context) ([]string, error) {
	return p.fetchModels(ctx, "")
}

func (p *oauthAIProvider) fetchModels(ctx context.Context, fallback string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.RLock()
	config, baseClient, resolver, source := cloneAIProviderConfig(p.config), p.client, p.resolver, p.quotaSupplier
	p.mu.RUnlock()
	if config.Supplier == "" {
		config.Supplier = fallback
	}
	// OpenAI subscription: public Codex catalog (no OAuth token); still uses proxy_group_id.
	if config.AuthType == AuthTypeOAuth && config.Supplier == "openai" {
		return p.fetchCodexModelsCatalog(ctx, config, baseClient, resolver)
	}
	endpointURL, service, err := modelsEndpoint(config, source)
	if err != nil {
		return nil, err
	}
	if service == "" && config.APIKey == "" || service != "" && (config.CredentialID == "" || p.manager == nil) {
		return nil, ErrModelsNotConfigured
	}
	client, err := p.modelsHTTPClient(ctx, config, baseClient, resolver, false)
	if err != nil {
		return nil, err
	}
	token := config.APIKey
	if service != "" {
		token, err = p.manager.GetValidAccessToken(ctx, service, config.CredentialID, client)
		if err != nil {
			return nil, ErrModelsAuthentication
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpointURL, nil)
		if err != nil {
			return nil, ErrModelsNotConfigured
		}
		req.Header.Set("Accept", "application/json")
		if service == oauth.OAuthServiceClaude || config.Supplier == "anthropic" && service == "" {
			req.Header.Set("Anthropic-Version", "2023-06-01")
		}
		if service == oauth.OAuthServiceClaude {
			req.Header.Set("anthropic-beta", "oauth-2025-04-20")
			req.Header.Set("User-Agent", "claude-code/2.1.7")
			req.Header.Set("Authorization", "Bearer "+token)
		} else if config.Supplier == "anthropic" && config.AuthType == AuthTypeAPIKey {
			req.Header.Del("Authorization")
			req.Header.Set("X-Api-Key", token)
		} else {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		body, status, err := doModelsGET(ctx, client, req)
		if err != nil {
			return nil, err
		}
		if status == http.StatusUnauthorized && service != "" && attempt == 0 {
			token, err = p.manager.RecoverAccessToken(ctx, service, config.CredentialID, token, client)
			if err != nil {
				return nil, ErrModelsAuthentication
			}
			continue
		}
		if err := modelsStatusError(status); err != nil {
			return nil, err
		}
		models, err := parseModelsResponse(body)
		if err != nil {
			return nil, err
		}
		return models, nil
	}
	return nil, ErrModelsAuthentication
}

// fetchCodexModelsCatalog loads model slugs from the public Codex models.json.
// Requests go through the account proxy group when proxy_group_id is set.
func (p *oauthAIProvider) fetchCodexModelsCatalog(ctx context.Context, config AIProviderConfig, baseClient *http.Client, resolver ProxyResolver) ([]string, error) {
	// Follow GitHub raw redirects (github.com/.../raw → raw.githubusercontent.com).
	client, err := p.modelsHTTPClient(ctx, config, baseClient, resolver, true)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, codexModelsJSONURL, nil)
	if err != nil {
		return nil, ErrModelsNotConfigured
	}
	req.Header.Set("Accept", "application/json")
	body, status, err := doModelsGET(ctx, client, req)
	if err != nil {
		return nil, err
	}
	if err := modelsStatusError(status); err != nil {
		return nil, err
	}
	return parseCodexModelsJSON(body)
}

func (p *oauthAIProvider) modelsHTTPClient(ctx context.Context, config AIProviderConfig, baseClient *http.Client, resolver ProxyResolver, followRedirects bool) (*http.Client, error) {
	if config.ProxyGroupID != 0 {
		if resolver == nil {
			return nil, ErrModelsNotConfigured
		}
		var selected *http.Client
		if !p.withProxy(ctx, resolver, config.ProxyGroupID, config.Supplier, func(c *http.Client) { selected = c }) || selected == nil {
			return nil, ErrModelsUpstream
		}
		baseClient = selected
	}
	if baseClient == nil {
		baseClient = upstreamClient(http.DefaultTransport.(*http.Transport).Clone())
	}
	client := *baseClient
	client.Timeout = 25 * time.Second
	if followRedirects {
		client.CheckRedirect = nil
	} else {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return &client, nil
}

func doModelsGET(ctx context.Context, client *http.Client, req *http.Request) (body []byte, status int, err error) {
	resp, err := client.Do(req)
	if err != nil {
		reportAPICall(ctx, httpExchangeTrace(req, nil, nil, true))
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, ErrModelsUpstream
	}
	status = resp.StatusCode
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	resp.Body.Close()
	reportAPICall(ctx, httpExchangeTrace(req, resp, body, false))
	if status == http.StatusOK && (readErr != nil || len(body) > 1<<20) {
		return nil, status, ErrModelsInvalidResponse
	}
	return body, status, nil
}

func modelsStatusError(status int) error {
	if status == http.StatusOK {
		return nil
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrModelsAuthentication
	case http.StatusTooManyRequests:
		return ErrModelsRateLimited
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return ErrModelsUnsupported
	default:
		return ErrModelsUpstream
	}
}

func modelsEndpoint(c AIProviderConfig, source func(string) Supplier) (endpoint, service string, err error) {
	base := strings.TrimSpace(c.APIEndpoint)
	if base == "" && source != nil && c.Supplier != "" {
		s := source(c.Supplier)
		// Prefer the OpenAI-compatible catalog URL; fall back to Claude.
		base = cmp.Or(strings.TrimSpace(s.OpenAIURL), strings.TrimSpace(s.ClaudeURL))
	}
	if base == "" {
		switch c.Supplier {
		case "anthropic":
			base = "https://api.anthropic.com/v1"
		case "openai":
			base = "https://api.openai.com/v1"
		case "grok":
			base = "https://api.x.ai/v1"
		case "deepseek":
			base = "https://api.deepseek.com/v1"
		case "zhipu":
			base = "https://open.bigmodel.cn/api/paas/v4"
		case "kimi":
			base = "https://api.moonshot.cn/v1"
		default:
			return "", "", ErrModelsNotConfigured
		}
	}
	parsed, parseErr := url.Parse(base)
	if parseErr != nil || parsed.Host == "" || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.User != nil {
		return "", "", ErrModelsNotConfigured
	}
	// Base URLs commonly end with /v1 (or vendor equivalent); append /models.
	endpoint = strings.TrimRight(base, "/") + "/models"
	if c.AuthType == AuthTypeOAuth {
		switch c.Supplier {
		case "anthropic":
			service = oauth.OAuthServiceClaude
		case "openai":
			// Handled by fetchCodexModelsCatalog; keep as safety if modelsEndpoint is called alone.
			return "", "", ErrModelsUnsupported
		case "grok":
			service = oauth.OAuthServiceGrok
		default:
			return "", "", ErrModelsUnsupported
		}
	}
	return endpoint, service, nil
}

func parseModelsResponse(body []byte) ([]string, error) {
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		// Some gateways return a bare string array.
		Models []string `json:"models"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		// Accept a top-level JSON array of model id strings.
		var ids []string
		if err2 := json.Unmarshal(body, &ids); err2 != nil {
			return nil, ErrModelsInvalidResponse
		}
		return normalizeModelIDs(ids), nil
	}
	if len(payload.Data) > 0 {
		ids := make([]string, 0, len(payload.Data))
		for _, item := range payload.Data {
			ids = append(ids, item.ID)
		}
		return normalizeModelIDs(ids), nil
	}
	if len(payload.Models) > 0 {
		return normalizeModelIDs(payload.Models), nil
	}
	return nil, ErrModelsInvalidResponse
}

// parseCodexModelsJSON reads Codex models-manager models.json (models[].slug).
func parseCodexModelsJSON(body []byte) ([]string, error) {
	var payload struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, ErrModelsInvalidResponse
	}
	if len(payload.Models) == 0 {
		return nil, ErrModelsInvalidResponse
	}
	ids := make([]string, 0, len(payload.Models))
	for _, item := range payload.Models {
		ids = append(ids, item.Slug)
	}
	return normalizeModelIDs(ids), nil
}

func normalizeModelIDs(ids []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	slices.Sort(out)
	if len(out) == 0 {
		return nil
	}
	if len(out) > 512 {
		out = out[:512]
	}
	return out
}
