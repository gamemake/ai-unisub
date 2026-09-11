package provider

import (
	"ai-unisub/internal/oauth"
	"context"
	"encoding/json"
	"net/http"
)

type ClaudeConfig struct {
	ProviderConfig
	OAuth OAuthConfig `json:"oauth"`
}
type ClaudeProvider struct {
	oauthProvider
	oauth OAuthConfig
}

func NewClaudeProvider(id string, raw json.RawMessage, manager *oauth.OAuthManager) (*ClaudeProvider, error) {
	var config ClaudeConfig
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
	return &ClaudeProvider{oauthProvider: base, oauth: config.OAuth}, nil
}
func ClaudeProviderFactory(manager *oauth.OAuthManager) ProviderFactory {
	return func(id string, raw json.RawMessage) (Provider, error) { return NewClaudeProvider(id, raw, manager) }
}
func (p *ClaudeProvider) UpdateConfig(raw json.RawMessage) error {
	var config ClaudeConfig
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
func (p *ClaudeProvider) Handle(r *http.Request, rec APICallRecorder) {
	p.handle(oauth.OAuthServiceClaude, p.oauth.CredentialID, r, rec)
}
func (p *ClaudeProvider) FetchUsage(ctx context.Context) ([]UsageItem, error) { return p.usage(ctx) }
func (p *ClaudeProvider) ResetUsage(ctx context.Context) error                { return p.reset(ctx) }
