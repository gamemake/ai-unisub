// Package service provides the composition and HTTP dispatch boundary.
package service

import (
	"ai-unisub/internal/database"
	"ai-unisub/internal/oauth"
	"ai-unisub/internal/oauth/adapters"
	"ai-unisub/internal/provider"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Config struct {
	DatabaseURL           string
	AdminUsername         string
	AdminPassword         string
	GatewayQueueLimit     int
	GatewayRequestTimeout time.Duration
	SessionTTL            time.Duration
	OAuthCallbackBaseURL  string
	ListenAddr            string
}
type Module interface {
	Name() string
	Init(ModuleContext) error
	Close() error
}
type Service struct {
	cfg       Config
	db        database.Database
	providers *provider.ProviderManager
	proxy     *ProxyManager
	oauth     *oauth.OAuthManager
	router    *router
	results   *OAuthResultStore
	modules   []Module
	authSvc   *authService
	mu        sync.Mutex
	closed    bool
}

func New(cfg Config) (*Service, error) {
	if cfg.DatabaseURL == "" {
		return nil, errors.New("database URL is required")
	}
	db, e := database.NewDatabase(cfg.DatabaseURL)
	if e != nil {
		return nil, fmt.Errorf("create database: %w", e)
	}
	if e = db.Open(); e != nil {
		return nil, fmt.Errorf("open database: %w", e)
	}
	return NewWithDependencies(cfg, db, provider.NewProviderManager())
}
func NewWithDependencies(cfg Config, db database.Database, p *provider.ProviderManager) (*Service, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	if p == nil {
		p = provider.NewProviderManager()
	}
	s := &Service{cfg: cfg, db: db, providers: p, proxy: NewProxyManager(db), oauth: oauth.NewManager(db), router: newRouter(), results: NewOAuthResultStore()}
	p.SetProxyResolver(s.proxy)
	s.authSvc = &authService{s: s, sessions: map[string]session{}}
	for _, adapter := range []oauth.OAuthAdapter{adapters.NewGrok(adapters.GrokConfig{}), adapters.NewCodex(adapters.CodexConfig{}), adapters.NewClaude(adapters.ClaudeConfig{}), oauth.NewDummyAdapter()} {
		if err := s.oauth.Register(adapter); err != nil {
			return nil, fmt.Errorf("register OAuth adapter: %w", err)
		}
	}
	factories := map[string]provider.ProviderFactory{
		"grok":   provider.GrokProviderFactory(s.oauth),
		"codex":  provider.CodexProviderFactory(s.oauth),
		"claude": provider.ClaudeProviderFactory(s.oauth),
		"dummy": func(id string, config json.RawMessage) (provider.Provider, error) {
			return provider.NewDummyProvider(id, config)
		},
	}
	for name, factory := range factories {
		if err := p.Register(name, factory); err != nil && !strings.Contains(err.Error(), "already registered") {
			return nil, fmt.Errorf("register provider %s: %w", name, err)
		}
	}
	return s, nil
}
func (s *Service) AddModule(m Module) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m == nil {
		return errors.New("service module is nil")
	}
	if s.closed {
		return errors.New("service is closed")
	}
	n := len(s.router.routes)
	if e := m.Init(&moduleContext{s: s}); e != nil {
		s.router.truncate(n)
		_ = m.Close()
		return fmt.Errorf("init service module %s: %w", m.Name(), e)
	}
	if e := s.router.err(); e != nil {
		s.router.truncate(n)
		_ = m.Close()
		return fmt.Errorf("register service module %s: %w", m.Name(), e)
	}
	s.modules = append(s.modules, m)
	return nil
}
func (s *Service) Handler() http.Handler                { return s.router.handler(s) }
func (s *Service) Config() Config                       { return s.cfg }
func (s *Service) Database() database.Database          { return s.db }
func (s *Service) Providers() *provider.ProviderManager { return s.providers }
func (s *Service) Proxy() *ProxyManager                 { return s.proxy }
func (s *Service) OAuth() *oauth.OAuthManager           { return s.oauth }
func (s *Service) Auth() AuthService                    { return s.authSvc }
func (s *Service) OAuthResults() *OAuthResultStore      { return s.results }
func (s *Service) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	ms := append([]Module(nil), s.modules...)
	s.mu.Unlock()
	var first error
	for i := len(ms) - 1; i >= 0; i-- {
		if e := ms[i].Close(); e != nil && first == nil {
			first = e
		}
	}
	if e := s.db.Close(); e != nil && first == nil {
		first = e
	}
	return first
}
