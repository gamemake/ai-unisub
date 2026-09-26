package unisub

import (
	"encoding/json"
)

// CCSwitchClient identifies one concrete CC Switch agent application.
type CCSwitchClient string

const (
	CCSwitchClaudeCode    CCSwitchClient = "claude_code"
	CCSwitchClaudeDesktop CCSwitchClient = "claude_desktop"
	CCSwitchCodex         CCSwitchClient = "codex"
	CCSwitchGrokBuild     CCSwitchClient = "grok_build"
)

type CCSwitchModelConfig struct {
	Model      string `json:"model"`
	Supports1M bool   `json:"supports_1m"`
}

type CCSwitchClientConfig struct {
	SupplierName string                         `json:"supplier_name"`
	Remark       string                         `json:"remark"`
	Models       map[string]CCSwitchModelConfig `json:"models,omitempty"`
	DefaultModel string                         `json:"default_model,omitempty"`
}

type APIKeyConfig struct {
	Version  int                                     `json:"version"`
	CCSwitch map[CCSwitchClient]CCSwitchClientConfig `json:"cc_switch,omitempty"`
}

func parseAPIKeyConfig(raw json.RawMessage) APIKeyConfig {
	config := APIKeyConfig{Version: 1, CCSwitch: map[CCSwitchClient]CCSwitchClientConfig{}}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &config); err != nil {
			config = APIKeyConfig{Version: 1, CCSwitch: map[CCSwitchClient]CCSwitchClientConfig{}}
		}
	}
	if config.Version == 0 {
		config.Version = 1
	}
	if config.CCSwitch == nil {
		config.CCSwitch = map[CCSwitchClient]CCSwitchClientConfig{}
	}
	return config
}

func parseCCSwitchClient(value string) (CCSwitchClient, error) {
	client := CCSwitchClient(value)
	switch client {
	case CCSwitchClaudeCode, CCSwitchClaudeDesktop, CCSwitchCodex, CCSwitchGrokBuild:
		return client, nil
	default:
		return "", errUnsupportedCCSwitchClient
	}
}
