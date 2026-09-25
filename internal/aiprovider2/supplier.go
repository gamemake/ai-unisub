package aiprovider2

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
)

const maxSupplierResponseBytes = 1 << 20

var (
	ErrModelsUnsupported  = errors.New("supplier does not support model listing")
	ErrQuotaUnsupported   = errors.New("supplier does not support quota queries")
	ErrUpstream           = errors.New("supplier upstream request failed")
	ErrAuthentication     = errors.New("supplier authentication failed")
	ErrRateLimited        = errors.New("supplier request rate limited")
	ErrInvalidResponse    = errors.New("invalid supplier response")
	ErrQuotaNotConfigured = errors.New("quota credentials or endpoint are not configured")
)

type SubscriptionPlanWeight struct {
	Name   string `json:"name"`
	Weight int    `json:"weight"`
}

// ModelMapping maps a client-visible model name to a model supported by the
// supplier. Pattern may contain any number of '*' wildcards. Target is always
// a literal model name from the supplier's effective Models list.
type ModelMapping struct {
	Pattern string `json:"pattern"`
	Target  string `json:"target"`
}

type SupplierBuiltinConfig struct {
	ClaudeURL string
	OpenAIURL string
	Models    []string
	Mappings  []ModelMapping
	Weights   []SubscriptionPlanWeight
}

type SupplierOverlayConfig struct {
	Models   []string
	Mappings []ModelMapping
	Weights  []SubscriptionPlanWeight
}

type SupplierConfig = SupplierBuiltinConfig

type Supplier interface {
	GetID() string
	GetName() string
	GetModels() []string
	GetBuiltinConfig() SupplierConfig                         // 获取代码中的内置的配置，用于恢复修改
	GetConfig() SupplierConfig                                // 获取实际的配置，包含修改和内置配置
	DiffConfig(SupplierConfig) (SupplierOverlayConfig, error) // 通过比较，计算出 SupplierOverlayConfig
	GetOverlayConfig() SupplierOverlayConfig
	SetOverlayConfig(overlay SupplierOverlayConfig, persist bool) error

	RefreshModel(ctx context.Context, account *Account) ([]string, error)
	FetchQuota(ctx context.Context, account *Account) (AccountQuota, error)
	ResetQuota(ctx context.Context, account *Account, resetType string) error
}

type SupplierData struct {
	manager *providerManager
	id      string
	name    string
	builtin SupplierBuiltinConfig // 代码中的内置的配置，用于恢复修改
	overlay SupplierOverlayConfig // 实际的配置，包含修改和内置配置
	mu      sync.RWMutex
}

func (s *SupplierData) GetID() string {
	return s.id
}

func (s *SupplierData) GetName() string {
	return s.name
}

func (s *SupplierData) GetModels() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	models := s.overlay.Models
	if models == nil {
		models = s.builtin.Models
	}
	return slices.Clone(models)
}

func (s *SupplierData) GetBuiltinConfig() SupplierConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneSupplierConfig(s.builtin)
}

func (s *SupplierData) GetConfig() SupplierConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	models := s.overlay.Models
	if models == nil {
		models = s.builtin.Models
	}
	weights := s.overlay.Weights
	if weights == nil {
		weights = s.builtin.Weights
	}
	mappings := s.overlay.Mappings
	if mappings == nil {
		mappings = s.builtin.Mappings
	}
	config := SupplierConfig{
		ClaudeURL: s.builtin.ClaudeURL,
		OpenAIURL: s.builtin.OpenAIURL,
		Models:    slices.Clone(models),
		Mappings:  slices.Clone(mappings),
		Weights:   slices.Clone(weights),
	}
	return config
}

func (s *SupplierData) DiffConfig(config SupplierConfig) (SupplierOverlayConfig, error) {
	s.mu.RLock()
	builtin := cloneSupplierConfig(s.builtin)
	s.mu.RUnlock()
	if config.ClaudeURL != builtin.ClaudeURL || config.OpenAIURL != builtin.OpenAIURL {
		return SupplierOverlayConfig{}, errors.New("supplier URLs are built in and cannot be changed")
	}
	if err := validateSupplierValues(config.Models, config.Mappings, config.Weights); err != nil {
		return SupplierOverlayConfig{}, err
	}
	var overlay SupplierOverlayConfig
	if !slices.Equal(config.Models, builtin.Models) {
		overlay.Models = slices.Clone(config.Models)
	}
	if !slices.Equal(config.Mappings, builtin.Mappings) {
		overlay.Mappings = slices.Clone(config.Mappings)
	}
	if !slices.Equal(config.Weights, builtin.Weights) {
		overlay.Weights = slices.Clone(config.Weights)
	}
	return overlay, nil
}

func (s *SupplierData) GetOverlayConfig() SupplierOverlayConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneOverlay(s.overlay)
}

func (s *SupplierData) SetOverlayConfig(overlay SupplierOverlayConfig, persist bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	models := overlay.Models
	if models == nil {
		models = s.builtin.Models
	}
	mappings := overlay.Mappings
	if mappings == nil {
		mappings = s.builtin.Mappings
	}
	weights := overlay.Weights
	if weights == nil {
		weights = s.builtin.Weights
	}
	if err := validateSupplierValues(models, mappings, weights); err != nil {
		return err
	}

	if persist {
		if err := s.manager.saveSupplier(s.id, overlay); err != nil {
			return err
		}
	}

	s.overlay = overlay
	return nil
}

func cloneSupplierConfig(config SupplierConfig) SupplierConfig {
	config.Models = slices.Clone(config.Models)
	config.Mappings = slices.Clone(config.Mappings)
	config.Weights = slices.Clone(config.Weights)
	return config
}

func cloneOverlay(overlay SupplierOverlayConfig) SupplierOverlayConfig {
	overlay.Models = slices.Clone(overlay.Models)
	overlay.Mappings = slices.Clone(overlay.Mappings)
	overlay.Weights = slices.Clone(overlay.Weights)
	return overlay
}

func validateSupplierValues(models []string, mappings []ModelMapping, weights []SubscriptionPlanWeight) error {
	seenModels := make(map[string]struct{}, len(models))
	for _, model := range models {
		trimmed := strings.TrimSpace(model)
		if trimmed == "" || trimmed != model {
			return errors.New("supplier model must not be empty")
		}
		if _, ok := seenModels[model]; ok {
			return errors.New("duplicate supplier model")
		}
		seenModels[model] = struct{}{}
	}
	for _, mapping := range mappings {
		pattern := strings.TrimSpace(mapping.Pattern)
		target := strings.TrimSpace(mapping.Target)
		if pattern == "" || pattern != mapping.Pattern || target == "" || target != mapping.Target {
			return errors.New("model mapping pattern and target must be non-empty without surrounding whitespace")
		}
		if _, ok := seenModels[target]; !ok {
			return errors.New("model mapping target must be a valid supplier model")
		}
	}
	seenWeights := make(map[string]struct{}, len(weights))
	for _, weight := range weights {
		if strings.TrimSpace(weight.Name) == "" || weight.Weight <= 0 {
			return errors.New("subscription weight must be positive and named")
		}
		if _, ok := seenWeights[weight.Name]; ok {
			return errors.New("duplicate subscription weight")
		}
		seenWeights[weight.Name] = struct{}{}
	}
	return nil
}

// MapModel applies the first matching mapping and otherwise returns model
// unchanged.
func MapModel(mappings []ModelMapping, model string) string {
	for _, mapping := range mappings {
		if matchModelPattern(mapping.Pattern, model) {
			return mapping.Target
		}
	}
	return model
}

func matchModelPattern(pattern, model string) bool {
	patternIndex := 0
	modelIndex := 0
	starIndex := -1
	starMatch := 0

	for modelIndex < len(model) {
		if patternIndex < len(pattern) && pattern[patternIndex] == model[modelIndex] {
			patternIndex++
			modelIndex++
			continue
		}
		if patternIndex < len(pattern) && pattern[patternIndex] == '*' {
			starIndex = patternIndex
			patternIndex++
			starMatch = modelIndex
			continue
		}
		if starIndex < 0 {
			return false
		}
		patternIndex = starIndex + 1
		starMatch++
		modelIndex = starMatch
	}

	for patternIndex < len(pattern) && pattern[patternIndex] == '*' {
		patternIndex++
	}
	return patternIndex == len(pattern)
}
