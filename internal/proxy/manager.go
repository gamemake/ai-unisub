package proxy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

var ErrUnavailable = errors.New("no available proxy in group")
var ErrClosed = errors.New("proxy manager is closed")
var ErrStatsCapacity = errors.New("proxy statistics capacity reached")

type Policy struct {
	FailureThreshold             int
	NetworkFailureThreshold      int
	ApplicationFailureThreshold  int
	Cooldown                     time.Duration
	HalfOpenLimit                int
	MaxBuckets                   int
	ProbeTimeout                 time.Duration
	Window                       time.Duration
	MinSamples                   int64
	NetworkFailureRate           float64
	ApplicationFailureRate       float64
	ApplicationCooldown          time.Duration
	NetworkRecoverySuccesses     int
	ApplicationRecoverySuccesses int
	MaxProbeInterval             time.Duration
	AutoProbe                    bool
	ProbeConcurrency             int
}

func DefaultPolicy() Policy {
	return Policy{FailureThreshold: 3, Cooldown: 30 * time.Second, HalfOpenLimit: 1, MaxBuckets: 10000, ProbeTimeout: 12 * time.Second, Window: 5 * time.Minute, MinSamples: 5, NetworkFailureRate: .5, ApplicationFailureRate: .5, ApplicationCooldown: 5 * time.Minute, NetworkRecoverySuccesses: 2, ApplicationRecoverySuccesses: 2, MaxProbeInterval: 10 * time.Minute, AutoProbe: true, ProbeConcurrency: 4}
}

type State struct {
	Status              string     `json:"status"`
	Requests            int64      `json:"requests"`
	Failures            int64      `json:"failures"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	LastSuccess         time.Time  `json:"last_success"`
	LastFailure         time.Time  `json:"last_failure"`
	CooldownUntil       time.Time  `json:"cooldown_until"`
	InFlight            int        `json:"half_open_in_flight"`
	LastProbe           time.Time  `json:"last_probe"`
	ProbeRequests       int64      `json:"probe_requests"`
	ProbeFailures       int64      `json:"probe_failures"`
	RecoverySuccesses   int        `json:"recovery_successes"`
	Backoff             int        `json:"backoff"`
	Probing             bool       `json:"probing"`
	LastError           ErrorClass `json:"last_error,omitempty"`
}
type stateKey struct{ address, app string }
type bucketKey struct {
	stateKey
	at time.Time
}
type ErrorClass string

const (
	Success            ErrorClass = ""
	NetworkError       ErrorClass = "network"
	ApplicationError   ErrorClass = "application"
	ApplicationIgnored ErrorClass = "application_ignored"
	Canceled           ErrorClass = "canceled"
)

type Prober func(context.Context, *Endpoint) error
type Manager struct {
	mu      sync.Mutex
	store   Store
	policy  Policy
	states  map[stateKey]*State
	minutes map[bucketKey]Bucket
	pending map[bucketKey]Bucket
	source  string
	now     func() time.Time
	prober  Prober
	stop    chan struct{}
	done    chan struct{}
	stopped bool
	closed  bool
	ctx     context.Context
	cancel  context.CancelFunc
	probes  sync.WaitGroup
	leases  map[string][]stateKey
}

func NewManager(store Store, policy Policy) *Manager {
	policy = normalizePolicy(policy)
	if policy.FailureThreshold < 1 || policy.Cooldown <= 0 || policy.HalfOpenLimit < 1 || policy.MaxBuckets < 2 || policy.ProbeTimeout <= 0 {
		panic("invalid proxy policy")
	}
	m := &Manager{store: store, policy: policy, states: map[stateKey]*State{}, minutes: map[bucketKey]Bucket{}, pending: map[bucketKey]Bucket{}, source: newID(), now: time.Now, prober: probe, stop: make(chan struct{}), done: make(chan struct{})}
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.leases = map[string][]stateKey{}
	go func() {
		defer close(m.done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		probeTicker := time.NewTicker(time.Second)
		defer probeTicker.Stop()
		for {
			select {
			case <-m.stop:
				return
			case <-ticker.C:
				_ = m.Flush()
			case <-probeTicker.C:
				m.startDueProbes()
			}
		}
	}()
	return m
}
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func clone[T any](v T) T { b, _ := json.Marshal(v); var out T; _ = json.Unmarshal(b, &out); return out }
func (m *Manager) List() ([]Group, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	groups, err := m.store.ListProxyGroups()
	if err != nil {
		return nil, err
	}
	groups = clone(groups)
	for i := range groups {
		for j := range groups[i].Proxies {
			p := &groups[i].Proxies[j]
			e, err := NewEndpoint(p.URL)
			if err != nil {
				continue
			}
			s := m.state(e.String(), "")
			network := *s
			p.Network = &network
			p.Applications = map[string]State{}
			for key, state := range m.states {
				if key.address == e.String() && key.app != "" {
					p.Applications[key.app] = *state
				}
			}
			p.Status = s.Status
			p.Available = s.Status == "available"
			if !s.LastSuccess.IsZero() {
				t := s.LastSuccess
				p.LastAvailable = &t
			}
			if !s.LastFailure.IsZero() {
				t := s.LastFailure
				p.LastErrorAt = &t
			}
		}
	}
	return groups, nil
}
func (m *Manager) Save(g *Group) error {
	if g == nil || strings.TrimSpace(g.ID) == "" || strings.TrimSpace(g.Name) == "" {
		return errors.New("proxy group ID and name are required")
	}
	if g.MaxRetries < 0 {
		return errors.New("max retries must not be negative")
	}
	value := clone(*g)
	seen := map[string]bool{}
	ids := map[string]bool{}
	for i := range value.Proxies {
		p := &value.Proxies[i]
		e, err := NewEndpoint(p.URL)
		if err != nil {
			return err
		}
		p.URL = e.String()
		if seen[p.URL] {
			return errors.New("duplicate proxy address")
		}
		seen[p.URL] = true
		if p.ID == "" {
			p.ID = newID()
		}
		if ids[p.ID] {
			return errors.New("duplicate proxy ID")
		}
		ids[p.ID] = true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return ErrClosed
	}
	if err := m.store.SaveProxyGroup(&value); err != nil {
		return err
	}
	*g = clone(value)
	return nil
}
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return ErrClosed
	}
	return m.store.DeleteProxyGroup(id)
}
func (m *Manager) state(address, app string) *State {
	k := stateKey{address, app}
	s := m.states[k]
	if s == nil {
		s = &State{Status: "unknown"}
		m.states[k] = s
	}
	return s
}
func (m *Manager) Snapshot(e *Endpoint, app string) State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return *m.state(e.String(), app)
}
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	if !m.stopped {
		m.stopped = true
		close(m.stop)
		m.cancel()
	}
	m.mu.Unlock()
	<-m.done
	m.probes.Wait()
	if err := m.Flush(); err != nil {
		return err
	}
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	return nil
}
