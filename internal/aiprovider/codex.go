package aiprovider

import (
	"ai-unisub/internal/oauth"
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

func NewCodexAIProvider(id string, raw json.RawMessage, manager *oauth.OAuthManager) (*CodexAIProvider, error) {
	var config CodexConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	base, err := newOAuthAIProvider(id, raw, manager)
	if err != nil {
		return nil, err
	}
	if config.OAuth.CredentialID == "" {
		config.OAuth.CredentialID = config.CredentialID
	}
	return &CodexAIProvider{oauthAIProvider: base, oauth: config.OAuth}, nil
}
func CodexAIProviderFactory(manager *oauth.OAuthManager) AIProviderFactory {
	return func(id string, raw json.RawMessage) (AIProvider, error) { return NewCodexAIProvider(id, raw, manager) }
}
func (p *CodexAIProvider) UpdateConfig(raw json.RawMessage) error {
	var config CodexConfig
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
func (p *CodexAIProvider) Handle(r *http.Request, rec APICallRecorder) {
	p.handle(oauth.OAuthServiceCodex, p.Config().CredentialID, r, rec)
}
func (p *CodexAIProvider) FetchUsage(ctx context.Context) ([]UsageItem, error) { return p.usage(ctx) }
func (p *CodexAIProvider) ResetUsage(ctx context.Context) error                { return p.reset(ctx) }
