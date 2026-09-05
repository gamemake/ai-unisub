package server

import (
	"context"
	"errors"
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
	cfg               config.Config
	repo              *repository.Repository
	engine            *gin.Engine
	accountClients    sync.Map
	signer            tokenSigner
	limiters          sync.Map
	rateMu            sync.Mutex
	rates             map[string]rateWindow
	requestLogCancel  context.CancelFunc
	requestLogWG      sync.WaitGroup
	grokOAuthMu       sync.Mutex
	grokOAuthFlows    map[string]*grokOAuthFlow
	grokRefreshLocks  sync.Map
	pkceOAuthMu       sync.Mutex
	pkceOAuthFlows    map[string]*pkceOAuthFlow
	oauthRefreshLocks sync.Map
}

type rateWindow struct {
	started time.Time
	count   int
}

var errConcurrencyQueueTimeout = errors.New("concurrency queue timeout")

func New(cfg config.Config, repo *repository.Repository) *Server {
	gin.SetMode(gin.ReleaseMode)
	if cfg.ConcurrencyQueueTimeout <= 0 {
		cfg.ConcurrencyQueueTimeout = config.DefaultConcurrencyQueueTimeout
	}
	if cfg.RequestLogRetentionDays <= 0 {
		cfg.RequestLogRetentionDays = 30
	}
	if cfg.GrokOAuth.Issuer == "" {
		cfg.GrokOAuth.Issuer = config.DefaultGrokOAuthIssuer
	}
	if cfg.GrokOAuth.ClientID == "" {
		cfg.GrokOAuth.ClientID = config.DefaultGrokOAuthClientID
	}
	if len(cfg.GrokOAuth.Scopes) == 0 {
		cfg.GrokOAuth.Scopes = append([]string(nil), config.DefaultGrokOAuthScopes...)
	}
	if cfg.GrokOAuth.ClientVersion == "" {
		cfg.GrokOAuth.ClientVersion = config.DefaultGrokOAuthClientVersion
	}
	if cfg.ClaudeOAuth.AuthorizeURL == "" {
		cfg.ClaudeOAuth.AuthorizeURL = config.DefaultClaudeOAuthAuthorizeURL
	}
	if cfg.ClaudeOAuth.TokenURL == "" {
		cfg.ClaudeOAuth.TokenURL = config.DefaultClaudeOAuthTokenURL
	}
	if cfg.ClaudeOAuth.RedirectURI == "" {
		cfg.ClaudeOAuth.RedirectURI = config.DefaultClaudeOAuthRedirectURI
	}
	if cfg.ClaudeOAuth.ClientID == "" {
		cfg.ClaudeOAuth.ClientID = config.DefaultClaudeOAuthClientID
	}
	if len(cfg.ClaudeOAuth.Scopes) == 0 {
		cfg.ClaudeOAuth.Scopes = append([]string(nil), config.DefaultClaudeOAuthScopes...)
	}
	if cfg.CodexOAuth.AuthorizeURL == "" {
		cfg.CodexOAuth.AuthorizeURL = config.DefaultCodexOAuthAuthorizeURL
	}
	if cfg.CodexOAuth.TokenURL == "" {
		cfg.CodexOAuth.TokenURL = config.DefaultCodexOAuthTokenURL
	}
	if cfg.CodexOAuth.RedirectURI == "" {
		cfg.CodexOAuth.RedirectURI = config.DefaultCodexOAuthRedirectURI
	}
	if cfg.CodexOAuth.ClientID == "" {
		cfg.CodexOAuth.ClientID = config.DefaultCodexOAuthClientID
	}
	if len(cfg.CodexOAuth.Scopes) == 0 {
		cfg.CodexOAuth.Scopes = append([]string(nil), config.DefaultCodexOAuthScopes...)
	}
	s := &Server{
		cfg: cfg, repo: repo, engine: gin.New(), signer: tokenSigner{key: cfg.AdminSigningKey()},
		rates: map[string]rateWindow{}, grokOAuthFlows: map[string]*grokOAuthFlow{},
		pkceOAuthFlows: map[string]*pkceOAuthFlow{},
	}
	s.routes()
	s.startRequestLogCleanup()
	return s
}

func (s *Server) Handler() http.Handler { return s.engine }

func (s *Server) routes() {
	s.engine.Use(gin.Recovery(), requestLogger())
	s.engine.GET("/", func(c *gin.Context) { c.Redirect(http.StatusTemporaryRedirect, "/home") })
	s.engine.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	s.engine.GET("/home", s.adminPage)
	s.engine.GET("/assets/admin.css", s.adminStyles)
	s.engine.GET("/assets/admin.js", s.adminScript)
	s.engine.POST("/api/login", s.login)

	admin := s.engine.Group("/api")
	admin.Use(s.requireAdmin())
	admin.PUT("/password", s.changePassword)
	admin.GET("/me", s.currentUser)
	admin.GET("/users", s.listUsers)
	admin.POST("/users", s.createUser)
	admin.PUT("/users/:id", s.updateUser)
	admin.POST("/users/:id/password", s.resetUserPassword)
	admin.DELETE("/users/:id", s.deleteUser)
	admin.GET("/accounts", s.listAccounts)
	admin.POST("/accounts", s.createAccount)
	admin.GET("/accounts/:id", s.getAccount)
	admin.PUT("/accounts/:id", s.updateAccount)
	admin.DELETE("/accounts/:id", s.deleteAccount)
	admin.POST("/accounts/:id/enable", s.enableAccount)
	admin.POST("/accounts/:id/disable", s.disableAccount)
	admin.PUT("/accounts/:id/proxy", s.updateAccountProxy)
	admin.PUT("/accounts/:id/concurrency-queue", s.updateConcurrencyQueue)
	admin.GET("/accounts/:id/usage", s.accountUsage)
	admin.POST("/accounts/:id/usage/refresh", s.refreshAccountUsage)
	admin.GET("/usage/summary", s.usageSummary)
	admin.GET("/api-keys", s.listAPIKeys)
	admin.GET("/api-keys/:id", s.getAPIKey)
	admin.POST("/api-keys", s.createAPIKey)
	admin.PUT("/api-keys/:id", s.updateAPIKey)
	admin.POST("/api-keys/:id/reset", s.resetAPIKey)
	admin.DELETE("/api-keys/:id", s.deleteAPIKey)
	admin.POST("/providers/grok/oauth/device/start", s.startGrokOAuthDevice)
	admin.POST("/providers/grok/oauth/device/poll", s.pollGrokOAuthDevice)
	admin.POST("/providers/claude/oauth/start", s.startClaudeOAuth)
	admin.POST("/providers/claude/oauth/exchange", s.exchangeClaudeOAuth)
	admin.POST("/providers/codex/oauth/start", s.startCodexOAuth)
	admin.POST("/providers/codex/oauth/exchange", s.exchangeCodexOAuth)
	admin.GET("/request-logs", s.listRequestLogs)
	admin.GET("/request-logs/:day/:id", s.getRequestLog)

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
		user, err := s.repo.GetUserByUsername(c.Request.Context(), claims.Subject)
		if err != nil || !user.Enabled {
			apiError(c, http.StatusUnauthorized, "unauthorized", "valid admin bearer token required")
			c.Abort()
			return
		}
		c.Set("admin_username", user.Username)
		c.Set("user_id", user.ID)
		c.Set("user_role", user.Role)
		c.Set("current_user", user)
		c.Next()
	}
}

func (s *Server) acquire(ctx context.Context, accountID int64, limit int, wait time.Duration) (func(), error) {
	if limit <= 0 {
		limit = 1
	}
	value, _ := s.limiters.LoadOrStore(accountID, make(chan struct{}, limit))
	ch := value.(chan struct{})
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	default:
	}
	if wait <= 0 {
		return nil, errConcurrencyQueueTimeout
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-timer.C:
		return nil, errConcurrencyQueueTimeout
	case <-ctx.Done():
		return nil, ctx.Err()
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
	c.Set("error_type", code)
	c.JSON(status, gin.H{"error": gin.H{"type": code, "message": message}})
}

func (s *Server) Shutdown(context.Context) error {
	if s.requestLogCancel != nil {
		s.requestLogCancel()
		s.requestLogWG.Wait()
	}
	s.accountClients.Range(func(_, value any) bool {
		if transport, ok := value.(*http.Client).Transport.(*http.Transport); ok {
			transport.CloseIdleConnections()
		}
		return true
	})
	s.grokOAuthMu.Lock()
	for id, flow := range s.grokOAuthFlows {
		closeHTTPClient(flow.Client)
		delete(s.grokOAuthFlows, id)
	}
	s.grokOAuthMu.Unlock()
	s.pkceOAuthMu.Lock()
	for id, flow := range s.pkceOAuthFlows {
		closeHTTPClient(flow.Client)
		delete(s.pkceOAuthFlows, id)
	}
	s.pkceOAuthMu.Unlock()
	return nil
}

func (s *Server) startRequestLogCleanup() {
	ctx, cancel := context.WithCancel(context.Background())
	s.requestLogCancel = cancel
	s.requestLogWG.Add(1)
	go func() {
		defer s.requestLogWG.Done()
		for {
			now := time.Now()
			next := nextRequestLogCleanup(now, time.Local)
			timer := time.NewTimer(time.Until(next))
			select {
			case <-timer.C:
				dropped, err := s.repo.CleanupRequestLogTables(ctx, time.Now(), time.Local, s.cfg.RequestLogRetentionDays)
				if err != nil && !errors.Is(err, context.Canceled) {
					slog.Error("request log cleanup failed", "error", err)
				} else if len(dropped) > 0 {
					slog.Info("expired request log tables dropped", "count", len(dropped), "tables", dropped)
				}
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return
			}
		}
	}()
}

func nextRequestLogCleanup(now time.Time, location *time.Location) time.Time {
	if location == nil {
		location = time.Local
	}
	local := now.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), local.Hour()+1, 0, 0, 0, location)
}

func newHTTPTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true, DisableCompression: true,
		MaxIdleConns: 100, MaxIdleConnsPerHost: 20, IdleConnTimeout: 90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 60 * time.Second,
	}
}
