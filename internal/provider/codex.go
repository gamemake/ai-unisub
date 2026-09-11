package provider

import (
	"ai-unisub/internal/oauth"
	"context"
	"encoding/json"
	"net/http"
)

type CodexConfig struct {
	ProviderConfig
	OAuth OAuthConfig `json:"oauth"`
}
type CodexProvider struct {
	oauthProvider
	oauth OAuthConfig
}

func NewCodexProvider(id string, raw json.RawMessage, manager *oauth.OAuthManager) (*CodexProvider, error) {
	var config CodexConfig
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
	return &CodexProvider{oauthProvider: base, oauth: config.OAuth}, nil
}
func CodexProviderFactory(manager *oauth.OAuthManager) ProviderFactory {
	return func(id string, raw json.RawMessage) (Provider, error) { return NewCodexProvider(id, raw, manager) }
}
func (p *CodexProvider) UpdateConfig(raw json.RawMessage) error {
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
func (p *CodexProvider) Handle(r *http.Request, rec APICallRecorder) {
	p.handle(oauth.OAuthServiceCodex, p.oauth.CredentialID, r, rec)
}
func (p *CodexProvider) FetchUsage(ctx context.Context) ([]UsageItem, error) { return p.usage(ctx) }
func (p *CodexProvider) ResetUsage(ctx context.Context) error                { return p.reset(ctx) }
