package aiprovider

import (
	proxyconfig "ai-unisub/internal/proxy"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

const (
	AuthTypeOAuth  = "oauth"
	AuthTypeAPIKey = "api_key"
)

func decodeAIProviderConfig(id string, raw json.RawMessage) (AIProviderConfig, error) {
	config := AIProviderConfig{Enabled: true}
	if len(raw) > 0 && string(raw) != "null" {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return AIProviderConfig{}, err
		}
		if err := json.Unmarshal(raw, &config); err != nil {
			return AIProviderConfig{}, err
		}
		if _, ok := fields["enabled"]; !ok {
			config.Enabled = true
		}
		if _, ok := fields["api_keys"]; ok {
			return AIProviderConfig{}, errors.New("provider api_keys is no longer supported")
		}
	}
	if id != "" {
		config.ID = id
	}
	if strings.TrimSpace(config.AuthType) == "" {
		config.AuthType = AuthTypeOAuth
	}
	config.AuthType = strings.ToLower(strings.TrimSpace(config.AuthType))
	if config.AuthType != AuthTypeOAuth && config.AuthType != AuthTypeAPIKey {
		return AIProviderConfig{}, errors.New("invalid provider auth_type")
	}
	if config.APIEndpoint != "" {
		config.APIEndpoint = strings.TrimSpace(config.APIEndpoint)
		if err := validateEndpoint(config.APIEndpoint); err != nil {
			return AIProviderConfig{}, err
		}
	}
	if err := validateProxy(config.Proxy); err != nil {
		return AIProviderConfig{}, err
	}
	config.APIKey = strings.TrimSpace(config.APIKey)
	if config.CredentialID == "" {
		var nested struct {
			OAuth OAuthConfig `json:"oauth"`
		}
		if json.Unmarshal(raw, &nested) == nil {
			config.CredentialID = nested.OAuth.CredentialID
		}
	}
	if config.AuthType == AuthTypeAPIKey && config.APIKey == "" {
		return AIProviderConfig{}, errors.New("provider api_key is required for api_key auth")
	}
	if config.AuthType == AuthTypeOAuth && config.APIKey != "" {
		return AIProviderConfig{}, errors.New("provider api_key requires api_key auth")
	}
	if config.MaxConcurrentConnections < 0 {
		return AIProviderConfig{}, errors.New("max_concurrent_connections must not be negative")
	}
	if config.MaxConcurrentConnections == 0 {
		config.MaxConcurrentConnections = 1
	}
	if config.QueueTimeoutSeconds < 0 {
		return AIProviderConfig{}, errors.New("queue_timeout_seconds must not be negative")
	}
	return config, nil
}

func validateProxy(proxy string) error {
	if strings.TrimSpace(proxy) == "" {
		return nil
	}
	_, err := proxyconfig.NewEndpoint(proxy)
	return err
}

func validateEndpoint(endpoint string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("invalid provider api endpoint")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return nil
	default:
		return errors.New("invalid provider api endpoint")
	}
}
