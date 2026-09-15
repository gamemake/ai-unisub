package aiprovider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
)

// DummyAIProvider is a local provider for demos and development. It does not
// contact an upstream service; it records the request and returns a trace.
type DummyAIProvider struct {
	mu     sync.RWMutex
	config AIProviderConfig
}

func NewDummyAIProvider(id string, raw json.RawMessage) (*DummyAIProvider, error) {
	config, err := decodeAIProviderConfig(id, raw)
	if err != nil {
		return nil, err
	}
	return &DummyAIProvider{config: config}, nil
}
func DummyAIProviderFactory(_ any) AIProviderFactory {
	return func(id string, raw json.RawMessage) (AIProvider, error) { return NewDummyAIProvider(id, raw) }
}
func (p *DummyAIProvider) Config() AIProviderConfig {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return cloneAIProviderConfig(p.config)
}
func (p *DummyAIProvider) UpdateConfig(raw json.RawMessage) error {
	config, err := decodeAIProviderConfig(p.Config().ID, raw)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.config = config
	p.mu.Unlock()
	return nil
}
func (p *DummyAIProvider) Handle(r *http.Request, recorder APICallRecorder) {
	body, _ := io.ReadAll(r.Body)
	if recorder != nil {
		recorder(&AIProviderCallTrace{URL: r.URL.String(), OriginalRequestHeaders: r.Header.Clone(), RequestBody: body, Model: "dummy-model", ResponseStatus: 200, ResponseHeaders: http.Header{"Content-Type": {"application/json"}}, ResponseBody: []byte(`{"model":"dummy-model","choices":[{"message":{"role":"assistant","content":"UniSub dummy response"}}]}`)})
	}
}
func (p *DummyAIProvider) FetchUsage(context.Context) ([]UsageItem, error) {
	return []UsageItem{{Name: "requests", Value: "0"}}, nil
}
func (p *DummyAIProvider) ResetUsage(context.Context) error { return nil }
