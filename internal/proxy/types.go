package proxy

import (
	"net/http"
	"time"
)

type ProxyGroupConfig struct {
	Name    string   `json:"name"`
	Proxies []string `json:"proxies"`
}

// ProxyGroupState contains runtime health information persisted with a group.
// Network health is shared by all applications; application health is isolated
// so an upstream-specific failure does not disable a proxy for other callers.
type ProxyGroupState struct {
	Proxies map[string]ProxyState `json:"proxies,omitempty"`
}

type ProxyState struct {
	Healthy      bool                             `json:"healthy"`
	LastSuccess  time.Time                        `json:"last_success,omitzero"`
	LastFailure  time.Time                        `json:"last_failure,omitzero"`
	Applications map[string]ProxyApplicationState `json:"applications,omitempty"`
}

type ProxyApplicationState struct {
	Healthy     bool      `json:"healthy"`
	LastSuccess time.Time `json:"last_success,omitzero"`
	LastFailure time.Time `json:"last_failure,omitzero"`
}

type ProxyGroup struct {
	ID     int              `json:"id"`
	Config ProxyGroupConfig `json:"config"`
	State  ProxyGroupState  `json:"state"`
	dirty  bool
}

type ProxyManager interface {
	Open() error
	Close() error
	List() []ProxyGroup
	Create(config ProxyGroupConfig) (id int, err error)
	Delete(id int) error
	Update(id int, config ProxyGroupConfig) error
	// Do uses body as the only request body source without copying it. req.Body
	// is ignored and remains owned by the caller. handle decides whether an HTTP
	// response represents an application-specific proxy failure. Every direct
	// or proxied attempt is persisted through the manager's database.
	Do(id int, app string, req *http.Request, body []byte, handle func(res *http.Response) error) (*http.Response, error)
}
