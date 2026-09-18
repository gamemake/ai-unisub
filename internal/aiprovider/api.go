package aiprovider

import (
	"ai-unisub/internal/oauth"
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// API providers use the chosen supplier's protocol, or OpenAI-compatible
// transport when a custom URL is supplied without a supplier.
type APIProvider struct{ *oauthAIProvider }

func APIProviderFactory(manager *oauth.OAuthManager) AIProviderFactory {
	return func(id int, data ProviderData) (AIProvider, error) {
		p, err := newOAuthAIProvider(id, data, manager)
		if err != nil {
			return nil, err
		}
		if p.Config().AuthType != AuthTypeAPIKey {
			return nil, errors.New("API provider requires api_key authentication")
		}
		return &APIProvider{p}, nil
	}
}
func (p *APIProvider) UpdateConfig(raw json.RawMessage) error {
	c, e := decodeAIProviderConfig(p.Config().ID, raw)
	if e != nil {
		return e
	}
	if c.AuthType != AuthTypeAPIKey {
		return errors.New("API provider requires api_key authentication")
	}
	return p.update(raw)
}
func (p *APIProvider) Handle(r *http.Request, rec APICallRecorder) {
	service := p.Config().Supplier
	if service == "anthropic" {
		service = oauth.OAuthServiceClaude
	}
	p.handle(service, "", r, rec)
}
func (p *APIProvider) FetchQuota(ctx context.Context) (*Quota, error) {
	return p.quota(ctx, "")
}
func (p *APIProvider) ResetUsage(ctx context.Context) error { return p.reset(ctx) }
