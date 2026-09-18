package proxy

import (
	"ai-unisub/internal/database"
	"encoding/json"
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
	db database.Database
}

func NewStoreProxyForDB(db database.Database) Store {
	return &StoreProxyForDB{db: db}
}

func (p *StoreProxyForDB) ListProxyGroups() ([]Group, error) {
	records, err := p.db.ListProxyGroups()
	if err != nil {
		return nil, err
	}
	groups := make([]Group, 0, len(records))
	for _, record := range records {
		group, err := groupFromPersisted(record)
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, nil
}

func (p *StoreProxyForDB) SaveProxyGroup(g *Group) error {
	record, err := groupToPersisted(g)
	if err != nil {
		return err
	}
	if err := p.db.SaveProxyGroup(record); err != nil {
		return err
	}
	g.ID = record.ID
	g.CreatedAt = record.CreatedAt
	g.UpdatedAt = record.UpdatedAt
	return nil
}

func (p *StoreProxyForDB) DeleteProxyGroup(id int) error {
	return p.db.DeleteProxyGroup(id)
}

func (p *StoreProxyForDB) SaveProxyStats([]Bucket) error {
	return nil
}

func (p *StoreProxyForDB) ListProxyStats(string, string, time.Time, time.Time) ([]Bucket, error) {
	return nil, nil
}

func groupToPersisted(g *Group) (*database.PersistedProxyGroup, error) {
	config, err := json.Marshal(g)
	if err != nil {
		return nil, err
	}
	return &database.PersistedProxyGroup{
		ID:        g.ID,
		Config:    config,
		State:     json.RawMessage(`{}`),
		CreatedAt: g.CreatedAt,
		UpdatedAt: g.UpdatedAt,
	}, nil
}

func groupFromPersisted(record database.PersistedProxyGroup) (Group, error) {
	var group Group
	if len(record.Config) > 0 && string(record.Config) != "null" {
		if err := json.Unmarshal(record.Config, &group); err != nil {
			return Group{}, err
		}
	}
	group.ID = record.ID
	group.CreatedAt = record.CreatedAt
	group.UpdatedAt = record.UpdatedAt
	return group, nil
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
