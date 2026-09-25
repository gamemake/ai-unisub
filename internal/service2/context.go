package service

import (
	"ai-unisub/internal/aiprovider2"
	"ai-unisub/internal/database"
	"ai-unisub/internal/oauth2"
	"ai-unisub/internal/proxy2"
	"net/http"
)

type AuthMode int

const (
	AuthNone AuthMode = iota
	AuthSession
	AuthAPIKey
	AuthSessionAdmin
)

type RouteOptions struct {
	Auth AuthMode
	Name string
}
type ModuleContext interface {
	Config() Config
	Handle(string, RouteOptions, http.Handler)
	HandleFunc(string, RouteOptions, http.HandlerFunc)
	Database() database.Database
	OAuth() oauth2.OAuthManager
	AIProviders() aiprovider2.ProviderManager
	Proxy() proxy2.ProxyManager
	Auth() AuthService
	OAuthResults() *OAuthResultStore
}
type moduleContext struct{ s *Service }

func (c *moduleContext) Config() Config                                          { return c.s.cfg }
func (c *moduleContext) Handle(p string, o RouteOptions, h http.Handler)         { c.s.router.add(p, o, h) }
func (c *moduleContext) HandleFunc(p string, o RouteOptions, h http.HandlerFunc) { c.Handle(p, o, h) }
func (c *moduleContext) Database() database.Database                             { return c.s.db }
func (c *moduleContext) OAuth() oauth2.OAuthManager                              { return c.s.oauth }
func (c *moduleContext) AIProviders() aiprovider2.ProviderManager                { return c.s.aiProviders }
func (c *moduleContext) Proxy() proxy2.ProxyManager                              { return c.s.proxy }
func (c *moduleContext) Auth() AuthService                                       { return c.s.authSvc }
func (c *moduleContext) OAuthResults() *OAuthResultStore                         { return c.s.results }
