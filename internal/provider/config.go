package provider

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

func decodeProviderConfig(id string, raw json.RawMessage) (ProviderConfig, error) {
	config := ProviderConfig{Enabled: true}
	if len(raw) > 0 && string(raw) != "null" {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return ProviderConfig{}, err
		}
		if err := json.Unmarshal(raw, &config); err != nil {
			return ProviderConfig{}, err
		}
		if _, ok := fields["enabled"]; !ok {
			config.Enabled = true
		}
	}
	if id != "" {
		config.ID = id
	}
	if config.MaxConcurrentConnections < 0 {
		return ProviderConfig{}, errors.New("max_concurrent_connections must not be negative")
	}
	if config.MaxConcurrentConnections == 0 {
		config.MaxConcurrentConnections = 1
	}
	if config.QueueTimeoutSeconds < 0 {
		return ProviderConfig{}, errors.New("queue_timeout_seconds must not be negative")
	}
	if err := validateProxy(config.Proxy); err != nil {
		return ProviderConfig{}, err
	}
	return config, nil
}

func validateProxy(proxy string) error {
	if strings.TrimSpace(proxy) == "" {
		return nil
	}
	proxyURL, err := url.Parse(proxy)
	if err != nil || proxyURL.Scheme == "" || proxyURL.Host == "" {
		return errors.New("invalid provider proxy")
	}
	switch strings.ToLower(proxyURL.Scheme) {
	case "http", "https", "socks5", "socks5h":
		return nil
	default:
		return errors.New("invalid provider proxy")
	}
}
