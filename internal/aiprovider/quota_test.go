package aiprovider

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestQuotaWithoutConfiguration(t *testing.T) {
	providers := []AIProvider{
		&ClaudeAIProvider{oauthAIProvider: &oauthAIProvider{}},
		&CodexAIProvider{oauthAIProvider: &oauthAIProvider{}},
		&GrokAIProvider{oauthAIProvider: &oauthAIProvider{}},
		&APIProvider{oauthAIProvider: &oauthAIProvider{}},
	}
	for _, provider := range providers {
		snapshot, err := provider.FetchQuota(t.Context())
		if snapshot != nil || !(errors.Is(err, ErrQuotaNotConfigured) || errors.Is(err, ErrQuotaUnsupported)) {
			t.Fatalf("%T fabricated usage: %+v, %v", provider, snapshot, err)
		}
		cached := provider.GetCachedQuota()
		if cached == nil || cached.CacheStatus != QuotaCacheMissing || len(cached.Items) != 0 || !cached.UpdatedAt.IsZero() {
			t.Fatalf("%T fabricated a cached snapshot: %+v", provider, cached)
		}
	}
}

func TestDummyQuota(t *testing.T) {
	provider := &DummyAIProvider{}
	before := time.Now()
	result, err := provider.FetchQuota(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.CacheStatus != QuotaCacheFresh || len(result.Subscription) != 2 || len(result.Items) != 0 {
		t.Fatalf("invalid demo result: %+v", result)
	}
	if result.UpdatedAt.Before(before) || result.UpdatedAt.After(time.Now()) {
		t.Fatalf("invalid update time: %v", result.UpdatedAt)
	}
	for i, name := range []string{"5h", "weekly"} {
		window := result.Subscription[i]
		duration := 5 * time.Hour
		if i == 1 {
			duration = 7 * 24 * time.Hour
		}
		if window.TimeDimension != name || window.Usage < 0 || window.Usage > 100 || !window.ResetAt.After(result.UpdatedAt) || window.ResetAt.After(result.UpdatedAt.Add(duration)) {
			t.Fatalf("invalid normalized window: %+v", window)
		}
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if result, err := provider.FetchQuota(ctx); result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled fetch returned %v, %v", result, err)
	}
	if cached := provider.GetCachedQuota(); !reflect.DeepEqual(cached, result) {
		t.Fatalf("dummy fetch should populate cache: %+v", cached)
	}
}

func TestMergeLegacyQuotaIntoState(t *testing.T) {
	quota := json.RawMessage(`{"cache_status":"fresh","subscription":[{"time_dimension":"weekly","usage":1}]}`)
	merged := MergeLegacyQuota(nil, quota)
	var state AIProviderState
	if err := json.Unmarshal(merged, &state); err != nil || len(state.Quota.Subscription) != 1 || state.Quota.Subscription[0].Usage != 1 {
		t.Fatalf("legacy quota was not wrapped in state: %s %v", merged, err)
	}
	keep := json.RawMessage(`{"quota":{"cache_status":"stale"}}`)
	if got := string(MergeLegacyQuota(keep, quota)); got != string(keep) {
		t.Fatalf("existing state quota was replaced: %s", got)
	}
}

func TestDummyQuotaPersistsSnapshot(t *testing.T) {
	provider := &DummyAIProvider{}
	var saved AIProviderQuota
	provider.setStateStore(func(s AIProviderState) error {
		saved = s.Quota
		return nil
	})
	result, err := provider.FetchQuota(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if saved.CacheStatus != QuotaCacheFresh || !reflect.DeepEqual(saved.Subscription, result.Subscription) {
		t.Fatalf("state store was not given the committed quota snapshot: %+v", saved)
	}
	provider.setStateStore(func(AIProviderState) error { return errors.New("disk full") })
	if _, err := provider.FetchQuota(t.Context()); !errors.Is(err, ErrQuotaPersist) {
		t.Fatalf("persist failure: %v", err)
	}
}

func TestQuotaConfigCopiesDoNotAlias(t *testing.T) {
	headers := map[string]string{"Accept": "application/json"}
	catalog := Catalog{Suppliers: []Supplier{{ID: "test", SubscriptionUsageHeaderOverrides: headers, APIUsageHeaderOverrides: headers}}}
	cloned := cloneCatalog(catalog)
	for _, copy := range []map[string]string{cloned.Suppliers[0].SubscriptionUsageHeaderOverrides, cloned.Suppliers[0].APIUsageHeaderOverrides} {
		copy["Accept"] = "changed"
	}
	if headers["Accept"] != "application/json" {
		t.Fatal("catalog copies share mutable usage configuration")
	}
}
