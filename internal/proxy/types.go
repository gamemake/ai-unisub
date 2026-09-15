package proxy

import "time"

// Group is a named pool of outbound proxy addresses.
type Group struct {
	Enabled    *bool     `json:"enabled,omitempty"`
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Remark     string    `json:"remark"`
	MaxRetries int       `json:"max_retries"`
	Proxies    []Entry   `json:"proxies"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type Entry struct {
	Network       *State           `json:"network,omitempty"`
	Applications  map[string]State `json:"applications,omitempty"`
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	URL           string           `json:"url"`
	Remark        string           `json:"remark"`
	Enabled       bool             `json:"enabled"`
	Status        string           `json:"status"`
	Available     bool             `json:"available"`
	LastAvailable *time.Time       `json:"last_available,omitempty"`
	LastErrorAt   *time.Time       `json:"last_error_at,omitempty"`
	ErrorRecords  []ErrorRecord    `json:"error_records,omitempty"`
}

type ErrorRecord struct {
	StartAt time.Time `json:"start_at"`
	Count   int       `json:"count"`
}
