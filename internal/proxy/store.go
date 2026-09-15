package proxy

import "time"

type Store interface {
	ListProxyGroups() ([]Group, error)
	SaveProxyGroup(*Group) error
	DeleteProxyGroup(string) error
	SaveProxyStats([]Bucket) error
	ListProxyStats(string, string, time.Time, time.Time) ([]Bucket, error)
}

// Source makes absolute snapshots idempotent across retries and process restarts.
type Bucket struct {
	Address     string    `json:"address"`
	Application string    `json:"application"`
	StartAt     time.Time `json:"start_at"`
	Source      string    `json:"source"`
	Requests    int64     `json:"requests"`
	Failures    int64     `json:"failures"`
}
