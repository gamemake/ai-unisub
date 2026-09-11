package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
)

// DummyProvider is a local provider for demos and development. It does not
// contact an upstream service; it records the request and returns a trace.
type DummyProvider struct{ config ProviderConfig }

func NewDummyProvider(id string, raw json.RawMessage) (*DummyProvider, error) {
	config, err := decodeProviderConfig(id, raw)
	if err != nil {
		return nil, err
	}
	return &DummyProvider{config: config}, nil
}
func DummyProviderFactory(_ any) ProviderFactory {
	return func(id string, raw json.RawMessage) (Provider, error) { return NewDummyProvider(id, raw) }
}
func (p *DummyProvider) Config() ProviderConfig { return p.config }
func (p *DummyProvider) UpdateConfig(raw json.RawMessage) error {
	config, err := decodeProviderConfig(p.config.ID, raw)
	if err != nil {
		return err
	}
	p.config = config
	return nil
}
func (p *DummyProvider) Handle(r *http.Request, recorder APICallRecorder) {
	body, _ := io.ReadAll(r.Body)
	if recorder != nil {
		recorder(&ProviderCallTrace{URL: r.URL.String(), RequestBody: body, Model: "dummy-model"})
	}
}
func (p *DummyProvider) FetchUsage(context.Context) ([]UsageItem, error) {
	return []UsageItem{{Name: "requests", Value: "0"}}, nil
}
func (p *DummyProvider) ResetUsage(context.Context) error { return nil }
