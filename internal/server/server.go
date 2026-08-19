package server

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/config"
	"github.com/ai-unisub/ai-unisub/internal/repository"
	"github.com/gin-gonic/gin"
)

type Server struct {
	cfg      config.Config
	repo     *repository.Repository
	engine   *gin.Engine
	clients  map[string]*http.Client
	signer   tokenSigner
	limiters sync.Map
	rateMu   sync.Mutex
	rates    map[string]rateWindow
}

type rateWindow struct {
	started time.Time
	count   int
}

func New(cfg config.Config, repo *repository.Repository) *Server {
	gin.SetMode(gin.ReleaseMode)
	s := &Server{
		cfg: cfg, repo: repo, engine: gin.New(), signer: tokenSigner{key: cfg.AdminSigningKey()},
		clients: map[string]*http.Client{},
		rates:   map[string]rateWindow{},
	}
	for _, provider := range []string{"claude", "codex", "grok"} {
		s.clients[provider] = &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true, DisableCompression: true,
				MaxIdleConns: 100, MaxIdleConnsPerHost: 20, IdleConnTimeout: 90 * time.Second,
				TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 60 * time.Second,
			},
			CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s.engine }

func (s *Server) routes() {
	s.engine.Use(gin.Recovery(), requestLogger())
	s.engine.GET("/", func(c *gin.Context) { c.Redirect(http.StatusTemporaryRedirect, "/admin") })
	s.engine.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	s.engine.GET("/admin", s.adminPage)
	s.engine.POST("/admin/login", s.login)

	admin := s.engine.Group("/admin")
	admin.Use(s.requireAdmin())
	admin.PUT("/password", s.changePassword)
	admin.GET("/accounts", s.listAccounts)
	admin.POST("/accounts", s.createAccount)
	admin.GET("/accounts/:id", s.getAccount)
	admin.DELETE("/accounts/:id", s.deleteAccount)
	admin.POST("/accounts/:id/enable", s.enableAccount)
	admin.POST("/accounts/:id/disable", s.disableAccount)
	admin.POST("/accounts/:id/api-key/reset", s.resetAPIKey)
	admin.GET("/accounts/:id/usage", s.accountUsage)
	admin.GET("/usage/summary", s.usageSummary)

	// Root endpoints resolve the provider exclusively from the account-bound key.
	s.engine.GET("/v1/models", s.proxyHandler("", routeModels, ""))
	s.engine.POST("/v1/messages", s.proxyHandler("claude", routeMessages, ""))
	s.engine.POST("/v1/messages/count_tokens", s.proxyHandler("claude", routeCountTokens, ""))
	s.engine.POST("/v1/responses", s.proxyHandler("", routeResponses, ""))
	s.engine.POST("/v1/responses/*subpath", s.proxyHandler("", routeResponses, "subpath"))

	s.engine.GET("/claude/v1/models", s.proxyHandler("claude", routeModels, ""))
	s.engine.POST("/claude/v1/messages", s.proxyHandler("claude", routeMessages, ""))
	s.engine.POST("/claude/v1/messages/count_tokens", s.proxyHandler("claude", routeCountTokens, ""))
	s.engine.GET("/codex/v1/models", s.proxyHandler("codex", routeModels, ""))
	s.engine.POST("/codex/v1/responses", s.proxyHandler("codex", routeResponses, ""))
	s.engine.POST("/codex/v1/responses/*subpath", s.proxyHandler("codex", routeResponses, "subpath"))
	s.engine.GET("/grok/v1/models", s.proxyHandler("grok", routeModels, ""))
	s.engine.POST("/grok/v1/responses", s.proxyHandler("grok", routeResponses, ""))
	s.engine.POST("/grok/v1/responses/*subpath", s.proxyHandler("grok", routeResponses, "subpath"))

	s.engine.NoRoute(func(c *gin.Context) { apiError(c, http.StatusNotFound, "not_found", "route not found") })
}

func (s *Server) requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := bearer(c.GetHeader("Authorization"))
		claims, err := s.signer.verify(token)
		if err != nil {
			apiError(c, http.StatusUnauthorized, "unauthorized", "valid admin bearer token required")
			c.Abort()
			return
		}
		c.Set("admin_username", claims.Subject)
		c.Next()
	}
}

func (s *Server) tryAcquire(accountID int64, limit int) (func(), bool) {
	if limit <= 0 {
		limit = 1
	}
	value, _ := s.limiters.LoadOrStore(accountID, make(chan struct{}, limit))
	ch := value.(chan struct{})
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, true
	default:
		return nil, false
	}
}

func (s *Server) allowRate(key string, limit int, window time.Duration) bool {
	if limit <= 0 {
		return true
	}
	now := time.Now()
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	state := s.rates[key]
	if state.started.IsZero() || now.Sub(state.started) >= window {
		state = rateWindow{started: now}
	}
	if state.count >= limit {
		s.rates[key] = state
		return false
	}
	state.count++
	s.rates[key] = state
	return true
}

func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()
		slog.Info("http_request", "method", c.Request.Method, "path", c.FullPath(), "status", c.Writer.Status(), "duration_ms", time.Since(started).Milliseconds(), "client_ip", clientIP(c.Request))
	}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func bearer(header string) string {
	parts := strings.SplitN(strings.TrimSpace(header), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func apiError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"error": gin.H{"type": code, "message": message}})
}

func (s *Server) Shutdown(context.Context) error {
	for _, client := range s.clients {
		if transport, ok := client.Transport.(*http.Transport); ok {
			transport.CloseIdleConnections()
		}
	}
	return nil
}
