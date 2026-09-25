// Package service provides the composition and HTTP dispatch boundary.
package service

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"ai-unisub/internal/aiprovider2"
	"ai-unisub/internal/database"
	"ai-unisub/internal/oauth2"
	"ai-unisub/internal/proxy2"
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
	cfg         Config
	db          database.Database
	aiProviders aiprovider2.ProviderManager
	proxy       proxy2.ProxyManager
	oauth       oauth2.OAuthManager
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
	db, err := database.NewDatabase(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("create database: %w", err)
	}
	if err := db.Open(); err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	service, err := NewWithDependencies(cfg, db, nil)
	if err != nil {
		_ = db.Close()
	}
	return service, err
}

func NewWithDependencies(cfg Config, db database.Database, providers aiprovider2.ProviderManager) (*Service, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	proxyManager := proxy2.NewManager(db)
	if err := proxyManager.Open(); err != nil {
		return nil, fmt.Errorf("open proxy manager: %w", err)
	}
	oauthManager := oauth2.NewManager(db, proxyManager)
	if providers == nil {
		var err error
		providers, err = aiprovider2.NewProviderManager(db, proxyManager, oauthManager)
		if err != nil {
			_ = proxyManager.Close()
			return nil, fmt.Errorf("create AI provider manager: %w", err)
		}
	}
	if err := providers.Open(); err != nil {
		_ = proxyManager.Close()
		return nil, fmt.Errorf("open AI provider manager: %w", err)
	}
	s := &Service{
		cfg: cfg, db: db, aiProviders: providers, proxy: proxyManager,
		oauth: oauthManager, router: newRouter(), results: NewOAuthResultStore(),
	}
	s.authSvc = &authService{s: s, sessions: map[string]session{}}
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
	if err := m.Init(&moduleContext{s: s}); err != nil {
		s.router.truncate(n)
		_ = m.Close()
		return fmt.Errorf("init service module %s: %w", m.Name(), err)
	}
	if err := s.router.err(); err != nil {
		s.router.truncate(n)
		_ = m.Close()
		return fmt.Errorf("register service module %s: %w", m.Name(), err)
	}
	s.modules = append(s.modules, m)
	return nil
}

func (s *Service) Handler() http.Handler                    { return s.router.handler(s) }
func (s *Service) Config() Config                           { return s.cfg }
func (s *Service) Database() database.Database              { return s.db }
func (s *Service) AIProviders() aiprovider2.ProviderManager { return s.aiProviders }
func (s *Service) Proxy() proxy2.ProxyManager               { return s.proxy }
func (s *Service) OAuth() oauth2.OAuthManager               { return s.oauth }
func (s *Service) Auth() AuthService                        { return s.authSvc }
func (s *Service) OAuthResults() *OAuthResultStore          { return s.results }

func (s *Service) Close() error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	s.mu.Lock()
	if s.closeDone {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	modules := slices.Clone(s.modules)
	s.mu.Unlock()
	var errs []error
	for _, module := range slices.Backward(modules) {
		errs = append(errs, module.Close())
	}
	errs = append(errs, s.aiProviders.Close(), s.proxy.Close(), s.db.Close())
	if err := errors.Join(errs...); err != nil {
		return err
	}
	s.mu.Lock()
	s.closeDone = true
	s.mu.Unlock()
	return nil
}
