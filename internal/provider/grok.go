package provider

import (
	"ai-unisub/internal/oauth"
	"context"
	"encoding/json"
	"net/http"
)

type GrokConfig struct {
	ProviderConfig
	OAuth OAuthConfig `json:"oauth"`
}

type GrokProvider struct {
	oauthProvider
	oauth OAuthConfig
}

func NewGrokProvider(id string, raw json.RawMessage, manager *oauth.OAuthManager) (*GrokProvider, error) {
	var config GrokConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	base, err := newOAuthProvider(id, raw, manager)
	if err != nil {
		return nil, err
	}
	if config.OAuth.CredentialID == "" {
		config.OAuth.CredentialID = config.CredentialID
	}
	return &GrokProvider{oauthProvider: base, oauth: config.OAuth}, nil
}
func GrokProviderFactory(manager *oauth.OAuthManager) ProviderFactory {
	return func(id string, raw json.RawMessage) (Provider, error) { return NewGrokProvider(id, raw, manager) }
}
func (p *GrokProvider) UpdateConfig(raw json.RawMessage) error {
	var config GrokConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return err
	}
	if config.OAuth.CredentialID == "" {
		config.OAuth.CredentialID = config.CredentialID
	}
	if err := p.update(raw); err != nil {
		return err
	}
	p.oauth = config.OAuth
	return nil
}
func (p *GrokProvider) Handle(r *http.Request, rec APICallRecorder) {
	p.handle(oauth.OAuthServiceGrok, p.oauth.CredentialID, r, rec)
}
func (p *GrokProvider) FetchUsage(ctx context.Context) ([]UsageItem, error) { return p.usage(ctx) }
func (p *GrokProvider) ResetUsage(ctx context.Context) error                { return p.reset(ctx) }
