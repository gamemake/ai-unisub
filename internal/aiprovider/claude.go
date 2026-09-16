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

func NewClaudeAIProvider(id string, raw json.RawMessage, manager *oauth.OAuthManager) (*ClaudeAIProvider, error) {
	var config ClaudeConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	base, err := newOAuthAIProvider(id, raw, manager)
	if err != nil {
		return nil, err
	}
	config.OAuth.CredentialID = cmp.Or(config.OAuth.CredentialID, config.CredentialID)
	return &ClaudeAIProvider{oauthAIProvider: base, oauth: config.OAuth}, nil
}
func ClaudeAIProviderFactory(manager *oauth.OAuthManager) AIProviderFactory {
	return func(id string, raw json.RawMessage) (AIProvider, error) { return NewClaudeAIProvider(id, raw, manager) }
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
func (p *ClaudeAIProvider) FetchUsage(ctx context.Context) ([]UsageItem, error) { return p.usage(ctx) }
func (p *ClaudeAIProvider) ResetUsage(ctx context.Context) error                { return p.reset(ctx) }
