package aiprovider

import (
	"context"
	"slices"
	"strings"
)

// FetchModels returns the models that can be used through an
// account. For a group this is the intersection of its members' models. A
// member's model mapping is applied while checking the intersection, so a
// client model is supported when its mapped upstream name is available.
func (m *AIProviderManager) FetchModels(ctx context.Context, id int) ([]ModelInfo, error) {
	if m == nil {
		return nil, ErrModelsUnsupported
	}
	m.mu.RLock()
	provider, ok := m.aiProviders[id]
	if !ok {
		m.mu.RUnlock()
		return nil, ErrModelsUnsupported
	}
	config := provider.Config()
	m.mu.RUnlock()
	if config.Kind != "group" {
		models, err := provider.FetchModels(ctx)
		if err != nil {
			return nil, err
		}
		return modelInfos(models), nil
	}

	memberModels := make([][]string, 0, len(config.Members))
	memberMappings := make([][]ModelMapping, 0, len(config.Members))
	for _, member := range config.Members {
		models, err := m.FetchModels(ctx, member.ID)
		if err != nil {
			return nil, err
		}
		memberModels = append(memberModels, modelIDs(models))
		memberMappings = append(memberMappings, m.modelMappingsForProvider(member.ID))
	}
	if len(memberModels) == 0 {
		return []ModelInfo{}, nil
	}

	candidates := make(map[string]struct{})
	for _, models := range memberModels {
		for _, model := range models {
			if model = strings.TrimSpace(model); model != "" {
				candidates[model] = struct{}{}
			}
		}
	}
	for _, mappings := range memberMappings {
		for _, mapping := range mappings {
			// Exact mappings can introduce a client-facing name that is not
			// present in any upstream response. Wildcard mappings cannot be
			// expanded without inventing model names.
			if !strings.Contains(mapping.From, "*") {
				candidates[mapping.From] = struct{}{}
			}
		}
	}
	models := make([]string, 0, len(candidates))
	for candidate := range candidates {
		supported := true
		for i, available := range memberModels {
			upstream := MapModel(memberMappings[i], candidate)
			if !slices.Contains(available, upstream) {
				supported = false
				break
			}
		}
		if supported {
			models = append(models, candidate)
		}
	}
	slices.Sort(models)
	return modelInfos(models), nil
}

// ModelsForProvider returns the configured model catalog for an account
// without contacting the upstream. Group accounts return the mapped
// intersection of their members' configured catalogs.
func (m *AIProviderManager) ModelsForProvider(id int) []ModelInfo {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	provider, ok := m.aiProviders[id]
	if !ok {
		m.mu.RUnlock()
		return nil
	}
	config := provider.Config()
	if config.Kind != "group" {
		models := m.catalogModelsLocked(config, m.adapters[id])
		m.mu.RUnlock()
		return modelInfos(models)
	}
	m.mu.RUnlock()

	memberModels := make([][]string, 0, len(config.Members))
	memberMappings := make([][]ModelMapping, 0, len(config.Members))
	for _, member := range config.Members {
		models := m.ModelsForProvider(member.ID)
		memberModels = append(memberModels, modelIDs(models))
		memberMappings = append(memberMappings, m.modelMappingsForProvider(member.ID))
	}
	return modelInfos(intersectMappedModels(memberModels, memberMappings))
}

func (m *AIProviderManager) catalogModelsLocked(config AIProviderConfig, adapter string) []string {
	supplier := config.Supplier
	if supplier == "" {
		supplier = SupplierForAdapter(adapter)
	}
	for _, catalogSupplier := range m.catalog.Suppliers {
		if catalogSupplier.ID == supplier {
			return slices.Clone(catalogSupplier.Models)
		}
	}
	return nil
}

func intersectMappedModels(memberModels [][]string, memberMappings [][]ModelMapping) []string {
	if len(memberModels) == 0 {
		return nil
	}
	candidates := make(map[string]struct{})
	for _, models := range memberModels {
		for _, model := range models {
			if model = strings.TrimSpace(model); model != "" {
				candidates[model] = struct{}{}
			}
		}
	}
	for _, mappings := range memberMappings {
		for _, mapping := range mappings {
			if !strings.Contains(mapping.From, "*") {
				candidates[mapping.From] = struct{}{}
			}
		}
	}
	models := make([]string, 0, len(candidates))
	for candidate := range candidates {
		supported := true
		for i, available := range memberModels {
			if !slices.Contains(available, MapModel(memberMappings[i], candidate)) {
				supported = false
				break
			}
		}
		if supported {
			models = append(models, candidate)
		}
	}
	slices.Sort(models)
	return models
}

func modelInfos(models []string) []ModelInfo {
	info := make([]ModelInfo, 0, len(models))
	for _, model := range models {
		info = append(info, ModelInfo{ID: model})
	}
	return info
}

func modelIDs(models []ModelInfo) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

func (m *AIProviderManager) modelMappingsForProvider(id int) []ModelMapping {
	m.mu.RLock()
	defer m.mu.RUnlock()
	provider, ok := m.aiProviders[id]
	if !ok {
		return nil
	}
	config := provider.Config()
	supplier := config.Supplier
	if supplier == "" {
		supplier = SupplierForAdapter(m.adapters[id])
	}
	for _, catalogSupplier := range m.catalog.Suppliers {
		if catalogSupplier.ID == supplier {
			return cloneModelMappings(catalogSupplier.ModelMappings)
		}
	}
	return nil
}
