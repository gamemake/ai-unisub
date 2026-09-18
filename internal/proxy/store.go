package proxy

import (
	"ai-unisub/internal/database"
	"time"
)

type Store interface {
	ListProxyGroups() ([]Group, error)
	SaveProxyGroup(*Group) error
	DeleteProxyGroup(int) error
	SaveProxyStats([]Bucket) error
	ListProxyStats(string, string, time.Time, time.Time) ([]Bucket, error)
}

type StoreProxyForDB struct {
	db *database.Database
}

func NewStoreProxyForDB(db *database.Database) Store {
	return &StoreProxyForDB{db: db}
}

func (p *StoreProxyForDB) ListProxyGroups() ([]Group, error) {
	return nil, nil
}

func (p *StoreProxyForDB) SaveProxyGroup(*Group) error {
	return nil
}

func (p *StoreProxyForDB) DeleteProxyGroup(int) error {
	return nil
}

func (p *StoreProxyForDB) SaveProxyStats([]Bucket) error {
	return nil
}

func (p *StoreProxyForDB) ListProxyStats(string, string, time.Time, time.Time) ([]Bucket, error) {
	return nil, nil
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
