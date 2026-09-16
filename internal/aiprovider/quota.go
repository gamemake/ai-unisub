package aiprovider

import (
	"errors"
	"time"
)

// ErrQuotaNotImplemented distinguishes a placeholder from a successful query.
var ErrQuotaNotImplemented = errors.New("quota query is not implemented")

// QuotaItem holds an original upstream field name and value text for
// non-subscription providers.
// Body values are raw JSON (including nested objects and string quotes); header values are raw text.
// Never parse Value for scheduling or quota arithmetic.
type QuotaItem struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	// Source is a local discriminator for multi-response queries, not an upstream field.
	Source string `json:"source,omitempty"`
}

// SubscriptionQuotaItem describes one subscription usage window.
type SubscriptionQuotaItem struct {
	TimeDimension string    `json:"time_dimension"` // For example, "5h" or "weekly".
	Usage         float64   `json:"usage"`          // Used percentage; zero is a valid value.
	ResetAt       time.Time `json:"reset_at"`
}

// Quota describes account allowances: subscription usage, limits and reset times,
// or monetary balance for non-subscription providers. It is not a normalized
// remaining allowance; historical usage and costs are excluded.
// It is shared by fetch and cache reads for an individual provider; groups
// have no quota. Subscription providers populate Subscription; other providers
// populate Items, never both. Items preserve original upstream values, except
// for supplier-specific display normalization such as DeepSeek balances.
// UpdatedAt is the last data update, not the cache-read time.
// Successful fetches are fresh. Cache misses return a non-nil result with
// missing status and no data or timestamp. Query errors are returned separately.
type Quota struct {
	Subscription []SubscriptionQuotaItem `json:"subscription,omitempty"`
	Items        []QuotaItem             `json:"items,omitempty"`
	CacheStatus  QuotaCacheStatus        `json:"cache_status"`
	UpdatedAt    time.Time               `json:"updated_at,omitzero"`
}

type QuotaCacheStatus string

const (
	QuotaCacheMissing QuotaCacheStatus = "missing"
	QuotaCacheFresh   QuotaCacheStatus = "fresh"
	QuotaCacheStale   QuotaCacheStatus = "stale"
)
