// Package service provides the composition and HTTP dispatch boundary.
package service

import (
	"ai-unisub/internal/aiprovider"
	"ai-unisub/internal/database"
	"ai-unisub/internal/oauth"
	"ai-unisub/internal/oauth/adapters"
	"ai-unisub/internal/proxy"
	"errors"
	"fmt"
	"net/http"
	"slices"
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
	ProxyPolicy           *proxy.Policy
}
type Module interface {
	Name() string
	Init(ModuleContext) error
	Close() error
}
type Service struct {
	cfg         Config
	db          database.Database
	aiProviders *aiprovider.AIProviderManager
	proxy       *proxy.Manager
	oauth       *oauth.OAuthManager
	router      *router
	results     *OAuthResultStore
	modules     []Module
	authSvc     *authService
	mu          sync.Mutex
	closed      bool
	closeMu     sync.Mutex
	closeDone   bool
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
	return NewWithDependencies(cfg, db, aiprovider.NewAIProviderManager())
}
func NewWithDependencies(cfg Config, db database.Database, p *aiprovider.AIProviderManager) (*Service, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	if p == nil {
		p = aiprovider.NewAIProviderManager()
	}
	policy := proxy.DefaultPolicy()
	if cfg.ProxyPolicy != nil {
		policy = *cfg.ProxyPolicy
	}
	proxyManager := proxy.NewManager(proxy.NewStoreProxyForDB(db), policy)
	s := &Service{cfg: cfg, db: db, aiProviders: p, proxy: proxyManager, oauth: oauth.NewManager(db), router: newRouter(), results: NewOAuthResultStore()}
	p.SetProxyResolver(s.proxy)
	if err := loadSupplierCatalog(db, p); err != nil {
		_ = s.proxy.Close()
		return nil, fmt.Errorf("load AI catalog: %w", err)
	}
	s.authSvc = &authService{s: s, sessions: map[string]session{}}
	for _, adapter := range []oauth.OAuthAdapter{adapters.NewGrok(adapters.GrokConfig{}), adapters.NewCodex(adapters.CodexConfig{}), adapters.NewClaude(adapters.ClaudeConfig{}), oauth.NewDummyAdapter()} {
		if err := s.oauth.Register(adapter); err != nil {
			return nil, fmt.Errorf("register OAuth adapter: %w", err)
		}
	}
	factories := map[string]aiprovider.AIProviderFactory{
		"api":    aiprovider.APIProviderFactory(s.oauth),
		"grok":   aiprovider.GrokAIProviderFactory(s.oauth),
		"codex":  aiprovider.CodexAIProviderFactory(s.oauth),
		"claude": aiprovider.ClaudeAIProviderFactory(s.oauth),
		"dummy": func(id int, data aiprovider.ProviderData) (aiprovider.AIProvider, error) {
			return aiprovider.NewDummyAIProvider(id, data)
		},
	}
	for name, factory := range factories {
		if err := p.Register(name, factory); err != nil && !strings.Contains(err.Error(), "already registered") {
			return nil, fmt.Errorf("register provider %s: %w", name, err)
		}
	}
	return s, nil
}

func loadSupplierCatalog(db database.Database, p *aiprovider.AIProviderManager) error {
	store := newSupplierOverlayStore(db)
	overlays, err := store.ListSupplierOverlays()
	if err != nil {
		return err
	}
	if len(overlays) == 0 {
		legacy, err := db.LoadConfig(database.ModuleConfigType, aiprovider.ModuleConfigKey)
		if err != nil {
			return err
		}
		if len(legacy.Value) > 0 {
			migrated, err := aiprovider.MigrateLegacyCatalogOverlays(legacy.Value)
			if err != nil {
				return err
			}
			for name, value := range migrated {
				if err := store.PutSupplierOverlay(name, value); err != nil {
					return err
				}
			}
		}
	}
	return p.SetOverlayStore(store)
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
func (s *Service) Handler() http.Handler                      { return s.router.handler(s) }
func (s *Service) Config() Config                             { return s.cfg }
func (s *Service) Database() database.Database                { return s.db }
func (s *Service) AIProviders() *aiprovider.AIProviderManager { return s.aiProviders }
func (s *Service) Proxy() *proxy.Manager                      { return s.proxy }
func (s *Service) OAuth() *oauth.OAuthManager                 { return s.oauth }
func (s *Service) Auth() AuthService                          { return s.authSvc }
func (s *Service) OAuthResults() *OAuthResultStore            { return s.results }
func (s *Service) Close() error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	s.mu.Lock()
	if s.closeDone {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	ms := slices.Clone(s.modules)
	s.mu.Unlock()
	var first error
	for _, m := range slices.Backward(ms) {
		if e := m.Close(); e != nil && first == nil {
			first = e
		}
	}
	if e := s.proxy.Close(); e != nil {
		return e
	}
	if e := s.db.Close(); e != nil && first == nil {
		first = e
	}
	if first == nil {
		s.mu.Lock()
		s.closeDone = true
		s.mu.Unlock()
	}
	return first
}
