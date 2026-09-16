package aiprovider

import (
	"ai-unisub/internal/oauth"
	"cmp"
	"context"
	"encoding/json"
	"net/http"
)

type GrokConfig struct {
	AIProviderConfig
	OAuth OAuthConfig `json:"oauth"`
}

type GrokAIProvider struct {
	*oauthAIProvider
	oauth OAuthConfig
}

func NewGrokAIProvider(id string, raw json.RawMessage, manager *oauth.OAuthManager) (*GrokAIProvider, error) {
	var config GrokConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	base, err := newOAuthAIProvider(id, raw, manager)
	if err != nil {
		return nil, err
	}
	config.OAuth.CredentialID = cmp.Or(config.OAuth.CredentialID, config.CredentialID)
	return &GrokAIProvider{oauthAIProvider: base, oauth: config.OAuth}, nil
}
func GrokAIProviderFactory(manager *oauth.OAuthManager) AIProviderFactory {
	return func(id string, raw json.RawMessage) (AIProvider, error) { return NewGrokAIProvider(id, raw, manager) }
}
func (p *GrokAIProvider) UpdateConfig(raw json.RawMessage) error {
	var config GrokConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return err
	}
	config.OAuth.CredentialID = cmp.Or(config.OAuth.CredentialID, config.CredentialID)
	if err := p.update(raw); err != nil {
		return err
	}
	p.oauth = config.OAuth
	return nil
}
func (p *GrokAIProvider) Handle(r *http.Request, rec APICallRecorder) {
	p.handle(oauth.OAuthServiceGrok, p.Config().CredentialID, r, rec)
}
func (p *GrokAIProvider) FetchUsage(ctx context.Context) ([]UsageItem, error) { return p.usage(ctx) }
func (p *GrokAIProvider) ResetUsage(ctx context.Context) error                { return p.reset(ctx) }
