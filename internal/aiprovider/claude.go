package aiprovider

import (
	"ai-unisub/internal/oauth"
	"cmp"
	"context"
	"encoding/json"
	"net/http"
)

type ClaudeConfig struct {
	AIProviderConfig
	OAuth OAuthConfig `json:"oauth"`
}
type ClaudeAIProvider struct {
	*oauthAIProvider
	oauth OAuthConfig
}

func NewClaudeAIProvider(id int, data ProviderData, manager *oauth.OAuthManager) (*ClaudeAIProvider, error) {
	var config ClaudeConfig
	if err := json.Unmarshal(data.Config, &config); err != nil {
		return nil, err
	}
	base, err := newOAuthAIProvider(id, data, manager)
	if err != nil {
		return nil, err
	}
	config.OAuth.CredentialID = cmp.Or(config.OAuth.CredentialID, config.CredentialID)
	return &ClaudeAIProvider{oauthAIProvider: base, oauth: config.OAuth}, nil
}
func ClaudeAIProviderFactory(manager *oauth.OAuthManager) AIProviderFactory {
	return func(id int, data ProviderData) (AIProvider, error) { return NewClaudeAIProvider(id, data, manager) }
}
func (p *ClaudeAIProvider) UpdateConfig(raw json.RawMessage) error {
	var config ClaudeConfig
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
func (p *ClaudeAIProvider) Handle(r *http.Request, rec APICallRecorder) {
	p.handle(oauth.OAuthServiceClaude, p.Config().CredentialID, r, rec)
}
func (p *ClaudeAIProvider) FetchQuota(ctx context.Context) (*Quota, error) {
	return p.quota(ctx, "anthropic")
}
func (p *ClaudeAIProvider) FetchModels(ctx context.Context) ([]string, error) {
	return p.fetchModels(ctx, "anthropic")
}
func (p *ClaudeAIProvider) ResetUsage(ctx context.Context) error { return p.reset(ctx) }
