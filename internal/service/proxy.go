package service

import (
	"ai-unisub/internal/common"
	"ai-unisub/internal/database"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ProxyManager owns proxy groups and also implements provider.ProxyResolver.
// It is intentionally available to all service modules through ModuleContext.
type ProxyManager struct {
	db   database.Database
	mu   sync.Mutex
	next map[string]int
}

func NewProxyManager(db database.Database) *ProxyManager {
	return &ProxyManager{db: db, next: map[string]int{}}
}
func (m *ProxyManager) List() ([]database.PersistedProxyGroup, error) {
	groups, err := m.db.ListProxyGroups()
	if err != nil {
		return nil, err
	}
	for gi := range groups {
		for pi := range groups[gi].Proxies {
			p := &groups[gi].Proxies[pi]
			if p.Status == "" {
				if p.Available {
					p.Status = "available"
				} else {
					p.Status = "unknown"
				}
			}
		}
	}
	return groups, nil
}
func (m *ProxyManager) Save(v *database.PersistedProxyGroup) error {
	if v == nil || strings.TrimSpace(v.ID) == "" {
		return errors.New("proxy group ID is required")
	}
	if strings.TrimSpace(v.Name) == "" {
		return errors.New("proxy group name is required")
	}
	if v.MaxRetries < 0 {
		return errors.New("max retries must not be negative")
	}
	for i := range v.Proxies {
		v.Proxies[i].Name = strings.TrimSpace(v.Proxies[i].Name)
		v.Proxies[i].URL = strings.TrimSpace(v.Proxies[i].URL)
		if _, err := common.ParseHTTPProxy(v.Proxies[i].URL); err != nil || v.Proxies[i].URL == "" {
			return errors.New(common.MessageInvalidProxy)
		}
		if v.Proxies[i].ID == "" {
			v.Proxies[i].ID = newProxyID()
		}
		if v.Proxies[i].Status == "" {
			if v.Proxies[i].Available {
				v.Proxies[i].Status = "available"
			} else {
				v.Proxies[i].Status = "unknown"
			}
		}
		switch v.Proxies[i].Status {
		case "available":
			v.Proxies[i].Available = true
		case "unavailable", "unknown":
			v.Proxies[i].Available = false
		default:
			return errors.New("invalid proxy status")
		}
	}
	return m.db.SaveProxyGroup(v)
}
func (m *ProxyManager) Delete(id string) error { return m.db.DeleteProxyGroup(id) }
func (m *ProxyManager) ErrorRecords(groupID, proxyID string) (*database.PersistedProxy, error) {
	groups, err := m.db.ListProxyGroups()
	if err != nil {
		return nil, err
	}
	for _, group := range groups {
		if group.ID != groupID {
			continue
		}
		for _, proxy := range group.Proxies {
			if proxy.ID == proxyID {
				return &database.PersistedProxy{ID: proxy.ID, URL: proxy.URL, LastErrorAt: proxy.LastErrorAt, ErrorRecords: proxy.ErrorRecords}, nil
			}
		}
		return nil, errors.New("proxy not found")
	}
	return nil, errors.New("proxy group not found")
}
func (m *ProxyManager) ResolveProxy(_ context.Context, groupID string) (string, error) {
	return m.resolve(groupID)
}
func (m *ProxyManager) ProxyRetryLimit(groupID string) int {
	groups, err := m.db.ListProxyGroups()
	if err != nil {
		return 0
	}
	for _, group := range groups {
		if group.ID != groupID {
			continue
		}
		available := 0
		for _, p := range group.Proxies {
			if p.Enabled && (p.Status == "available" || (p.Status == "" && p.Available)) {
				available++
			}
		}
		if available < 2 || group.MaxRetries < 1 {
			return 0
		}
		if group.MaxRetries > available-1 {
			return available - 1
		}
		return group.MaxRetries
	}
	return 0
}
func (m *ProxyManager) resolve(groupID string) (string, error) {
	if strings.TrimSpace(groupID) == "" {
		return "", nil
	}
	groups, err := m.db.ListProxyGroups()
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, g := range groups {
		if g.ID != groupID {
			continue
		}
		available := []database.PersistedProxy{}
		for _, p := range g.Proxies {
			if p.Enabled && (p.Status == "available" || (p.Status == "" && p.Available)) {
				available = append(available, p)
			}
		}
		if len(available) == 0 {
			return "", errors.New("no available proxy in group")
		}
		i := m.next[groupID] % len(available)
		m.next[groupID]++
		return available[i].URL, nil
	}
	return "", errors.New("proxy group not found")
}
func (m *ProxyManager) ReportProxy(groupID string, proxyURL string, available bool) {
	if groupID == "" || proxyURL == "" {
		return
	}
	groups, err := m.db.ListProxyGroups()
	if err != nil {
		return
	}
	for i := range groups {
		if groups[i].ID != groupID {
			continue
		}
		for j := range groups[i].Proxies {
			if groups[i].Proxies[j].URL == proxyURL {
				if available {
					groups[i].Proxies[j].Status = "available"
				} else {
					groups[i].Proxies[j].Status = "unavailable"
					m.recordError(&groups[i].Proxies[j], time.Now().UTC())
				}
				groups[i].Proxies[j].Available = available
				if available {
					now := time.Now().UTC()
					groups[i].Proxies[j].LastAvailable = &now
				}
				_ = m.db.SaveProxyGroup(&groups[i])
				return
			}
		}
	}
}

// Test checks one proxy from the service side and persists its resulting status.
func (m *ProxyManager) Test(ctx context.Context, groupID, proxyID string) (*database.PersistedProxy, error) {
	groups, err := m.db.ListProxyGroups()
	if err != nil {
		return nil, err
	}
	for gi := range groups {
		if groups[gi].ID != groupID {
			continue
		}
		for pi := range groups[gi].Proxies {
			p := &groups[gi].Proxies[pi]
			if p.ID != proxyID {
				continue
			}
			result, err := m.testURL(ctx, p.URL)
			ok := err == nil && result.Status == "available"
			if ok {
				p.Status = "available"
				p.Available = true
				now := time.Now().UTC()
				p.LastAvailable = &now
			} else {
				p.Status = "unavailable"
				p.Available = false
				m.recordError(p, time.Now().UTC())
			}
			if err = m.db.SaveProxyGroup(&groups[gi]); err != nil {
				return nil, err
			}
			value := *p
			return &value, nil
		}
	}
	return nil, errors.New("proxy not found")
}

func (m *ProxyManager) recordError(proxy *database.PersistedProxy, now time.Time) {
	if proxy == nil {
		return
	}
	cutoff := now.Add(-24 * time.Hour)
	kept := proxy.ErrorRecords[:0]
	for _, record := range proxy.ErrorRecords {
		if !record.StartAt.Before(cutoff) {
			kept = append(kept, record)
		}
	}
	proxy.ErrorRecords = kept
	bucket := now.UTC().Truncate(10 * time.Minute)
	for i := range proxy.ErrorRecords {
		if proxy.ErrorRecords[i].StartAt.Equal(bucket) {
			proxy.ErrorRecords[i].Count++
			proxy.LastErrorAt = &now
			return
		}
	}
	proxy.ErrorRecords = append(proxy.ErrorRecords, database.ProxyErrorRecord{StartAt: bucket, Count: 1})
	proxy.LastErrorAt = &now
}

// TestURL tests a proxy URL that has not been saved in a proxy group yet.
func (m *ProxyManager) TestURL(ctx context.Context, proxyURL string) (*database.PersistedProxy, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	if _, err := common.ParseHTTPProxy(proxyURL); err != nil {
		return nil, err
	}
	result, err := m.testURL(ctx, proxyURL)
	if err != nil {
		return nil, err
	}
	return &database.PersistedProxy{URL: proxyURL, Status: result.Status, Available: result.Status == "available", LastAvailable: result.LastAvailable}, nil
}

func (m *ProxyManager) testURL(ctx context.Context, proxyURL string) (*database.PersistedProxy, error) {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(u)
	client := &http.Client{Transport: transport, Timeout: 12 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.gstatic.com/generate_204", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if resp != nil {
		resp.Body.Close()
	}
	ok := err == nil && resp != nil && resp.StatusCode < 500
	result := &database.PersistedProxy{URL: proxyURL, Status: "unavailable"}
	if ok {
		result.Status = "available"
		now := time.Now().UTC()
		result.LastAvailable = &now
	}
	return result, nil
}
func newProxyID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().String()))
	}
	return hex.EncodeToString(b[:])
}

type ProxyModule struct{}

func NewProxyModule() *ProxyModule              { return &ProxyModule{} }
func (m *ProxyModule) Name() string             { return "proxy" }
func (m *ProxyModule) Close() error             { return nil }
func (m *ProxyModule) Init(ModuleContext) error { return nil }
