package aiprovider

import (
	"context"
	"encoding/json"
	"io"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"
)

// DummyAIProvider is a local provider for demos and development. It does not
// contact an upstream service; it records the request and returns a trace.
type DummyAIProvider struct {
	mu         sync.RWMutex
	config     AIProviderConfig
	quotaCache quotaCache
}

func NewDummyAIProvider(id int, data ProviderData) (*DummyAIProvider, error) {
	config, err := decodeAIProviderConfig(id, data.Config)
	if err != nil {
		return nil, err
	}
	return &DummyAIProvider{config: config}, nil
}
func DummyAIProviderFactory(_ any) AIProviderFactory {
	return func(id int, data ProviderData) (AIProvider, error) { return NewDummyAIProvider(id, data) }
}
func (p *DummyAIProvider) Config() AIProviderConfig {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return cloneAIProviderConfig(p.config)
}
func (p *DummyAIProvider) State() AIProviderState {
	return AIProviderState{Quota: p.quotaCache.snapshot()}
}
func (p *DummyAIProvider) Quota() AIProviderQuota { return p.quotaCache.snapshot() }
func (p *DummyAIProvider) RestoreState(raw json.RawMessage) error {
	return restoreAIProviderState(&p.quotaCache, raw)
}
func (p *DummyAIProvider) UpdateConfig(raw json.RawMessage) error {
	config, err := decodeAIProviderConfig(p.Config().ID, raw)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.config = config
	p.quotaCache.invalidate()
	p.mu.Unlock()
	return p.quotaCache.save()
}
func (p *DummyAIProvider) Handle(r *http.Request, recorder APICallRecorder) {
	body, _ := io.ReadAll(r.Body)
	if recorder != nil {
		recorder(&AIProviderCallTrace{URL: r.URL.String(), OriginalRequestHeaders: r.Header.Clone(), RequestBody: body, Model: "dummy-model", ResponseStatus: 200, ResponseHeaders: http.Header{"Content-Type": {"application/json"}}, ResponseBody: []byte(`{"model":"dummy-model","choices":[{"message":{"role":"assistant","content":"UniSub dummy response"}}]}`)})
	}
}
func (p *DummyAIProvider) FetchQuota(ctx context.Context) (*Quota, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	p.mu.RLock()
	stamp := p.quotaCache.begin()
	p.mu.RUnlock()
	// The demo uses the same normalized 5h/weekly windows as Claude.
	result := &Quota{
		Subscription: []SubscriptionQuotaItem{
			{TimeDimension: "5h", Usage: float64(rand.IntN(101)), ResetAt: now.Add(time.Duration(1+rand.IntN(300)) * time.Minute)},
			{TimeDimension: "weekly", Usage: float64(rand.IntN(101)), ResetAt: now.Add(time.Duration(1+rand.IntN(10080)) * time.Minute)},
		},
		CacheStatus: QuotaCacheFresh,
		UpdatedAt:   now,
	}
	stamp.observed = now
	updates := []subscriptionUpdate{
		{key: "five_hour", item: result.Subscription[0], hasUsage: true, hasReset: true},
		{key: "seven_day", item: result.Subscription[1], hasUsage: true, hasReset: true},
	}
	if !p.quotaCache.putSubscription(stamp, updates, false) {
		return nil, ErrQuotaSuperseded
	}
	if err := p.quotaCache.save(); err != nil {
		return nil, err
	}
	reportAPICall(ctx, &AIProviderCallTrace{
		URL:                    "dummy://quota",
		OutboundRequestHeaders: http.Header{"Accept": {"application/json"}},
		ResponseStatus:         http.StatusOK,
		ResponseHeaders:        http.Header{"Content-Type": {"application/json"}},
		ResponseBody:           []byte(`{"dummy":"quota"}`),
	})
	return result, nil
}
func (p *DummyAIProvider) setStateStore(store func(AIProviderState) error) {
	p.quotaCache.setPersist(store)
}
func (p *DummyAIProvider) GetCachedQuota() *Quota {
	return p.quotaCache.get()
}
func (p *DummyAIProvider) FetchModels(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	models := []string{"dummy-fast", "dummy-long-context", "dummy-model", "dummy-reasoning"}
	reportAPICall(ctx, &AIProviderCallTrace{
		URL:                    "dummy://models",
		OutboundRequestHeaders: http.Header{"Accept": {"application/json"}},
		ResponseStatus:         http.StatusOK,
		ResponseHeaders:        http.Header{"Content-Type": {"application/json"}},
		ResponseBody:           []byte(`{"models":["dummy-fast","dummy-long-context","dummy-model","dummy-reasoning"]}`),
	})
	return models, nil
}
func (p *DummyAIProvider) ResetUsage(context.Context) error { return nil }
