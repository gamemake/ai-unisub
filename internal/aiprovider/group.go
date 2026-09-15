package aiprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"
)

type GroupMember struct {
	ID     string `json:"id"`
	Weight int    `json:"weight"`
}
type groupProvider struct {
	mu     sync.RWMutex
	config AIProviderConfig
}

func newGroup(id string, raw json.RawMessage) (AIProvider, error) {
	c, e := decodeAIProviderConfig(id, raw)
	if e != nil {
		return nil, e
	}
	return &groupProvider{config: c}, nil
}
func (g *groupProvider) Config() AIProviderConfig {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return cloneAIProviderConfig(g.config)
}
func (g *groupProvider) UpdateConfig(raw json.RawMessage) error {
	c, e := decodeAIProviderConfig(g.Config().ID, raw)
	if e != nil {
		return e
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.config = c
	return nil
}
func (g *groupProvider) Handle(_ *http.Request, rec APICallRecorder) {
	if rec != nil {
		rec(&AIProviderCallTrace{HTTPErrorCode: 503, HTTPErrorInfo: "group requires member routing"})
	}
}
func (g *groupProvider) FetchUsage(context.Context) ([]UsageItem, error) {
	return nil, errors.New("usage is recorded per member")
}
func (g *groupProvider) ResetUsage(context.Context) error {
	return errors.New("reset usage per member")
}

func prepareConfig(id, adapter string, raw json.RawMessage) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	delete(fields, "client_types")
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	if adapter == "group" {
		fields["kind"] = json.RawMessage(`"group"`)
	}
	if adapter == "api" {
		fields["kind"] = json.RawMessage(`"api"`)
		fields["auth_type"] = json.RawMessage(`"api_key"`)
	}
	raw, _ = json.Marshal(fields)
	c, err := decodeAIProviderConfig(id, raw)
	if err != nil {
		return nil, err
	}
	supplier := SupplierForAdapter(adapter)
	if c.Kind == "subscription" && adapter != "dummy" {
		if c.Supplier != "" && c.Supplier != supplier {
			return nil, errors.New("subscription supplier is fixed by account")
		}
		c.Supplier = supplier
	}
	if c.Supplier == "" && adapter != "api" {
		c.Supplier = supplier
	}
	if c.Supplier != "" {
		found := false
		for _, s := range BuiltinSuppliers() {
			if s.ID == c.Supplier {
				found = true
			}
		}
		if !found {
			return nil, errors.New("unknown supplier")
		}
	}
	if c.Kind == "api" && c.Supplier == "" && c.APIEndpoint == "" {
		return nil, errors.New("API URL or supplier is required")
	}
	if adapter == "group" {
		var explicit struct {
			Members []struct {
				Weight *int `json:"weight"`
			} `json:"members"`
		}
		if err := json.Unmarshal(raw, &explicit); err != nil {
			return nil, err
		}
		for _, member := range explicit.Members {
			if member.Weight != nil && (*member.Weight < 1 || *member.Weight > 5) {
				return nil, errors.New("member weight must be an integer from 1 to 5")
			}
		}
		if c.APIKey != "" || c.CredentialID != "" || c.Supplier != "" || c.APIEndpoint != "" || c.ProxyGroupID != "" || c.OfficialOnly {
			return nil, errors.New("groups cannot own credentials, supplier, endpoint or proxy group")
		}
		if len(c.Members) == 0 {
			return nil, errors.New("group requires members")
		}
		seen := map[string]bool{}
		for i := range c.Members {
			member := &c.Members[i]
			if member.Weight == 0 {
				member.Weight = 3
			}
			if member.ID == "" || member.ID == id || seen[member.ID] || member.Weight < 1 || member.Weight > 5 {
				return nil, errors.New("invalid group member or weight")
			}
			seen[member.ID] = true
		}
	} else if c.Kind == "group" {
		return nil, errors.New("group kind requires group provider")
	}
	// Keep provider-specific configuration fields, including compatibility data.
	normalized, _ := json.Marshal(c)
	var values map[string]json.RawMessage
	_ = json.Unmarshal(normalized, &values)
	for k, v := range values {
		fields[k] = v
	}
	return json.Marshal(fields)
}

func (m *AIProviderManager) validateRelations(id string, c AIProviderConfig) error {
	configs := map[string]AIProviderConfig{}
	for key, p := range m.aiProviders {
		configs[key] = p.Config()
	}
	configs[id] = c
	for _, group := range configs {
		if group.Kind != "group" {
			continue
		}
		for _, member := range group.Members {
			child, ok := configs[member.ID]
			if !ok || child.Kind == "group" {
				return errors.New("group members must be existing non-group providers")
			}
			if !AllowsClient(group, effectiveClient(child)) {
				return errors.New("group client type must be Any or match every member client type")
			}
		}
	}
	return nil
}
func (m *AIProviderManager) Referenced(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, p := range m.aiProviders {
		for _, member := range p.Config().Members {
			if member.ID == id {
				return true
			}
		}
	}
	return false
}

type affinityBinding struct {
	id   string
	last time.Time
}
type memberHealth struct {
	failures int
	until    time.Time
	paused   bool
	inFlight bool
}
type Selection struct {
	revision uint64
	ID       string
	Adapter  string
	Provider AIProvider
	Account  *Account
	binding  string
	leased   bool
}

// Select serializes affinity creation with health admission across groups.
func (m *AIProviderManager) Select(id, user string, headers http.Header, path string, tried []string) (Selection, error) {
	session, err := SessionID(headers)
	if err != nil {
		return Selection{}, err
	}
	client := DetectClient(headers)
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.aiProviders[id]
	if !ok || !p.Config().Enabled {
		return Selection{}, ErrUnavailable
	}
	c := p.Config()
	if !AllowsClient(c, client) {
		return Selection{}, ErrClientDenied
	}
	members := c.Members
	if c.Kind != "group" {
		members = []GroupMember{{ID: id, Weight: 3}}
	}
	now := time.Now()
	var choices []string
	weight := 0
	for _, member := range members {
		child, exists := m.aiProviders[member.ID]
		if !exists {
			continue
		}
		cc := child.Config()
		if !cc.Enabled || !AllowsClient(cc, client) || (c.Kind == "group" && !compatibleProtocol(cc, m.adapters[member.ID], path)) {
			continue
		}
		skip := false
		for _, t := range tried {
			if t == member.ID {
				skip = true
			}
		}
		if skip {
			continue
		}
		health := m.health[member.ID]
		if health != nil && (health.paused || now.Before(health.until) || health.inFlight) {
			continue
		}
		if member.Weight > weight {
			weight = member.Weight
			choices = nil
		}
		if member.Weight == weight {
			choices = append(choices, member.ID)
		}
	}
	if len(choices) == 0 {
		return Selection{}, ErrUnavailable
	}
	key := ""
	selected := ""
	if session != "" && c.Kind == "group" {
		sum := sha256.Sum256([]byte(user + "\x00" + id + "\x00" + string(client) + "\x00" + session))
		key = hex.EncodeToString(sum[:])
		for k, b := range m.bindings {
			if now.Sub(b.last) >= 30*time.Minute {
				delete(m.bindings, k)
			}
		}
		b := m.bindings[key]
		for _, choice := range choices {
			if choice == b.id {
				selected = choice
			}
		}
	}
	if selected == "" {
		selected = choices[rand.IntN(len(choices))]
	}
	if key != "" {
		if len(m.bindings) >= 10000 {
			var oldest string
			var at time.Time
			for k, b := range m.bindings {
				if oldest == "" || b.last.Before(at) {
					oldest, at = k, b.last
				}
			}
			delete(m.bindings, oldest)
		}
		m.bindings[key] = affinityBinding{selected, now}
	}
	leased := false
	if h := m.health[selected]; h != nil && !h.until.IsZero() {
		h.inFlight = true
		leased = true
	}
	return Selection{ID: selected, Adapter: m.adapters[selected], Provider: m.aiProviders[selected], Account: m.accounts[selected], binding: key, leased: leased, revision: m.revisions[selected]}, nil
}
func compatibleProtocol(c AIProviderConfig, adapter, path string) bool {
	if adapter == "dummy" {
		return true
	}
	if c.AuthType == AuthTypeAPIKey && c.Supplier != "" && c.APIEndpoint == "" {
		for _, s := range SupplierConfigs() {
			if s.ID == c.Supplier {
				return s.URLForClient(EndpointClient(path)) != ""
			}
		}
		return false
	}
	anthropic := c.Supplier == "anthropic" || adapter == "claude"
	if strings.HasSuffix(path, "/messages") || strings.HasSuffix(path, "/messages/count_tokens") {
		return anthropic
	}
	if strings.HasSuffix(path, "/chat/completions") || strings.HasSuffix(path, "/responses") {
		return !anthropic
	}
	return true
}

func (m *AIProviderManager) ReportSelection(s Selection, trace *AIProviderCallTrace, canceled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if revision, ok := m.revisions[s.ID]; !ok || revision != s.revision {
		return
	}
	h := m.health[s.ID]
	if h == nil {
		h = &memberHealth{}
		m.health[s.ID] = h
	}
	if h.inFlight && !s.leased {
		return
	}
	if s.leased {
		h.inFlight = false
	}
	if canceled || trace == nil {
		return
	}
	status := trace.ResponseStatus
	if status == 0 {
		status = trace.HTTPErrorCode
	}
	failed := false
	now := time.Now()
	var failure struct {
		Error struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	_ = json.Unmarshal(trace.ResponseBody, &failure)
	switch {
	case failure.Error.Code == "insufficient_quota" || failure.Error.Type == "insufficient_quota":
		h.paused = true
		failed = true
	case status == 401:
		h.paused = true
		failed = true
	case status == 429:
		h.until = now.Add(30 * time.Second)
		if retry := trace.ResponseHeaders.Get("Retry-After"); retry != "" {
			if seconds, e := time.ParseDuration(retry + "s"); e == nil && seconds > 0 {
				h.until = now.Add(seconds)
			} else if at, e := http.ParseTime(retry); e == nil && at.After(now) {
				h.until = at
			}
		}
		failed = true
	case status >= 500 || (status == 0 && trace.HTTPErrorInfo != ""):
		h.failures++
		failed = true
		if h.failures >= 3 || s.leased {
			delay := 30 * time.Second * time.Duration(1<<min(max(h.failures-3, 0), 4))
			h.until = now.Add(min(delay, 5*time.Minute))
		}
	case status >= 200 && status < 400:
		*h = memberHealth{}
	default: // Request-level errors do not quarantine an account.
	}
	if failed && s.binding != "" {
		if b := m.bindings[s.binding]; b.id == s.ID {
			delete(m.bindings, s.binding)
		}
	}
}
