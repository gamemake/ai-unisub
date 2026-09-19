package aiprovider

import (
	"ai-unisub/internal/oauth"
	"cmp"
	"context"
	"encoding/json"
	"net/http"
)

type CodexConfig struct {
	AIProviderConfig
	OAuth OAuthConfig `json:"oauth"`
}
type CodexAIProvider struct {
	*oauthAIProvider
	oauth OAuthConfig
}

func NewCodexAIProvider(id int, data ProviderData, manager *oauth.OAuthManager) (*CodexAIProvider, error) {
	var config CodexConfig
	if err := json.Unmarshal(data.Config, &config); err != nil {
		return nil, err
	}
	base, err := newOAuthAIProvider(id, data, manager)
	if err != nil {
		return nil, err
	}
	config.OAuth.CredentialID = cmp.Or(config.OAuth.CredentialID, config.CredentialID)
	return &CodexAIProvider{oauthAIProvider: base, oauth: config.OAuth}, nil
}
func CodexAIProviderFactory(manager *oauth.OAuthManager) AIProviderFactory {
	return func(id int, data ProviderData) (AIProvider, error) { return NewCodexAIProvider(id, data, manager) }
}
func (p *CodexAIProvider) UpdateConfig(raw json.RawMessage) error {
	var config CodexConfig
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
func (p *CodexAIProvider) Handle(r *http.Request, rec APICallRecorder) {
	p.handle(oauth.OAuthServiceCodex, p.Config().CredentialID, r, rec)
}
func (p *CodexAIProvider) FetchQuota(ctx context.Context) (*Quota, error) {
	return p.quota(ctx, "openai")
}
func (p *CodexAIProvider) FetchModels(ctx context.Context) ([]string, error) {
	return p.fetchModels(ctx, "openai")
}
func (p *CodexAIProvider) ResetUsage(ctx context.Context) error { return p.reset(ctx) }
