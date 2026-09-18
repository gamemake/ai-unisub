package aiprovider

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

const quotaCacheTTL = 5 * time.Minute

// Per-instance, bounded by the supplier's current display fields. Never persisted
// with credentials. Generation and request sequence reject obsolete responses.
type quotaCache struct {
	mu                                 sync.Mutex
	generation, sequence, fullSequence uint64
	entries                            []quotaCacheEntry
	windows                            []subscriptionCacheEntry
	persist                            func(AIProviderState) error
}

type subscriptionCacheEntry struct {
	update        subscriptionUpdate
	observed      time.Time
	resetObserved time.Time
	sequence      uint64
}

type quotaCacheEntry struct {
	item     QuotaItem
	observed time.Time
	sequence uint64
}

type quotaStamp struct {
	generation, sequence uint64
	observed             time.Time
}

func (c *quotaCache) begin() quotaStamp {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sequence++
	return quotaStamp{c.generation, c.sequence, time.Now().UTC()}
}

func (c *quotaCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	c.entries = nil
	c.windows = nil
	c.fullSequence = 0
}

func (c *quotaCache) put(stamp quotaStamp, items []QuotaItem, partial bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if stamp.generation != c.generation || stamp.sequence < c.fullSequence || len(items) == 0 {
		return false
	}
	c.windows = nil
	if !partial {
		kept := c.entries[:0]
		for _, entry := range c.entries {
			if entry.sequence > stamp.sequence {
				kept = append(kept, entry)
			}
		}
		c.entries = kept
		c.fullSequence = stamp.sequence
	}
	for _, item := range items {
		found := false
		for i, entry := range c.entries {
			if entry.item.Name != item.Name || entry.item.Source != item.Source {
				continue
			}
			found = true
			if entry.sequence <= stamp.sequence {
				c.entries[i] = quotaCacheEntry{item, stamp.observed, stamp.sequence}
			}
			break
		}
		if !found && len(c.entries) < 128 {
			c.entries = append(c.entries, quotaCacheEntry{item, stamp.observed, stamp.sequence})
		}
	}
	return true
}

func (c *quotaCache) setPersist(store func(AIProviderState) error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.persist = store
}

func (c *quotaCache) save() error {
	c.mu.Lock()
	store := c.persist
	q := c.getLocked()
	snap := AIProviderState{Quota: AIProviderQuota{Subscription: q.Subscription, Items: q.Items, CacheStatus: q.CacheStatus, UpdatedAt: q.UpdatedAt}}
	c.mu.Unlock()
	if store == nil {
		return nil
	}
	if err := store(snap); err != nil {
		if errors.Is(err, ErrQuotaPersist) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrQuotaPersist, err)
	}
	return nil
}

func (c *quotaCache) get() *Quota {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.getLocked()
}

func (c *quotaCache) getLocked() *Quota {
	result := &Quota{CacheStatus: QuotaCacheMissing}
	if len(c.windows) != 0 {
		for _, entry := range c.windows {
			if !entry.update.hasUsage {
				continue
			}
			if result.CacheStatus == QuotaCacheMissing {
				result.CacheStatus = QuotaCacheFresh
			}
			result.Subscription = append(result.Subscription, entry.update.item)
			if entry.observed.After(result.UpdatedAt) {
				result.UpdatedAt = entry.observed
			}
			if time.Since(entry.observed) >= quotaCacheTTL || !entry.update.item.ResetAt.IsZero() && time.Since(entry.resetObserved) >= quotaCacheTTL {
				result.CacheStatus = QuotaCacheStale
			}
		}
		return result
	}
	if len(c.entries) == 0 {
		return result
	}
	result.CacheStatus = QuotaCacheFresh
	now := time.Now()
	for _, entry := range c.entries {
		result.Items = append(result.Items, entry.item)
		if entry.observed.After(result.UpdatedAt) {
			result.UpdatedAt = entry.observed
		}
		if now.Sub(entry.observed) >= quotaCacheTTL {
			result.CacheStatus = QuotaCacheStale
		}
	}
	return result
}

func (c *quotaCache) snapshot() AIProviderQuota {
	q := c.get()
	return AIProviderQuota{Subscription: q.Subscription, Items: q.Items, CacheStatus: q.CacheStatus, UpdatedAt: q.UpdatedAt}
}

func (c *quotaCache) restore(value AIProviderQuota) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = nil
	c.windows = nil
	stamp := time.Now().UTC()
	if !value.UpdatedAt.IsZero() {
		stamp = value.UpdatedAt
	}
	if len(value.Subscription) != 0 {
		for _, item := range value.Subscription {
			c.windows = append(c.windows, subscriptionCacheEntry{
				update:   subscriptionUpdate{key: item.TimeDimension, item: item, hasUsage: true, hasReset: !item.ResetAt.IsZero()},
				observed: stamp, resetObserved: stamp, sequence: 1,
			})
		}
	} else {
		for _, item := range value.Items {
			c.entries = append(c.entries, quotaCacheEntry{item: item, observed: stamp, sequence: 1})
		}
	}
	c.generation++
	c.sequence = 1
	c.fullSequence = 1
}

func (c *quotaCache) putSubscription(stamp quotaStamp, updates []subscriptionUpdate, partial bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if stamp.generation != c.generation || stamp.sequence < c.fullSequence || len(updates) == 0 || len(updates) > 128 {
		return false
	}
	c.entries = nil
	if !partial {
		kept := c.windows[:0]
		for _, entry := range c.windows {
			if entry.sequence > stamp.sequence {
				kept = append(kept, entry)
			}
		}
		c.windows = kept
		c.fullSequence = stamp.sequence
	}
	for _, update := range updates {
		found := false
		for i := range c.windows {
			entry := &c.windows[i]
			if entry.update.key != update.key {
				continue
			}
			found = true
			if entry.sequence > stamp.sequence {
				break
			}
			if update.item.TimeDimension != "" {
				entry.update.item.TimeDimension = update.item.TimeDimension
			}
			if update.hasUsage {
				entry.update.item.Usage, entry.update.hasUsage = update.item.Usage, true
				entry.observed = stamp.observed
			}
			if update.hasReset {
				entry.update.item.ResetAt, entry.update.hasReset = update.item.ResetAt, true
				entry.resetObserved = stamp.observed
			}
			entry.sequence = stamp.sequence
			break
		}
		if !found && len(c.windows) < 128 {
			if update.item.TimeDimension == "" {
				update.item.TimeDimension = update.key
			}
			c.windows = append(c.windows, subscriptionCacheEntry{update: update, observed: stamp.observed, resetObserved: stamp.observed, sequence: stamp.sequence})
		}
	}
	return true
}
