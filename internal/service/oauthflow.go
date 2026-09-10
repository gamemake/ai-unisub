package service

import (
	"ai-unisub2/internal/oauth"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"
)

// OAuthFlowModule exposes the Web OAuth API and upstream callback endpoints.
// The OAuth protocol itself remains implemented by internal/oauth adapters.
type OAuthFlowModule struct{}

func NewOAuthFlowModule() *OAuthFlowModule { return &OAuthFlowModule{} }
func (m *OAuthFlowModule) Name() string    { return "oauthflow" }
func (m *OAuthFlowModule) Close() error    { return nil }

func (m *OAuthFlowModule) Init(ctx ModuleContext) error {
	ctx.HandleFunc("/api/oauth/", RouteOptions{Auth: AuthSession, Name: "oauth-api"}, func(w http.ResponseWriter, r *http.Request) {
		m.api(ctx, w, r)
	})
	for _, path := range []string{"/auth/callback", "/callback", "/oauth/code/callback"} {
		ctx.HandleFunc(path, RouteOptions{Auth: AuthNone, Name: "oauth-callback"}, func(w http.ResponseWriter, r *http.Request) {
			m.callback(ctx, w, r)
		})
	}
	return nil
}

func (m *OAuthFlowModule) api(ctx ModuleContext, w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, "/"), "/api/oauth/"), "/")
	if len(parts) == 2 && parts[0] != "results" && parts[1] == "start" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		m.start(ctx, w, r, parts[0])
		return
	}
	if len(parts) == 3 && parts[0] != "results" && parts[1] == "poll" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		m.poll(ctx, w, r, parts[0], parts[2])
		return
	}
	if len(parts) == 2 && parts[0] == "results" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		m.result(ctx, w, r, parts[1])
		return
	}
	http.NotFound(w, r)
}

func (m *OAuthFlowModule) start(ctx ModuleContext, w http.ResponseWriter, r *http.Request, service string) {
	p, ok := PrincipalFromContext(r.Context())
	if !ok || p.User == nil {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !validOAuthService(service) {
		WriteError(w, http.StatusBadRequest, "unsupported OAuth service")
		return
	}
	redirect := callbackURL(ctx.Config(), r, service)
	result, err := ctx.OAuth().Start(r.Context(), service, p.User.ID, redirect)
	if err != nil {
		oauthAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (m *OAuthFlowModule) poll(ctx ModuleContext, w http.ResponseWriter, r *http.Request, service, sessionID string) {
	p, ok := PrincipalFromContext(r.Context())
	if !ok || p.User == nil {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !validOAuthService(service) || sessionID == "" {
		WriteError(w, http.StatusBadRequest, "invalid OAuth session")
		return
	}
	session, err := ctx.OAuth().SessionForSubject(sessionID, service, p.User.ID)
	if err != nil {
		oauthAPIError(w, err)
		return
	}
	credential, err := ctx.OAuth().Poll(r.Context(), sessionID)
	if err != nil {
		if errors.Is(err, oauth.ErrAuthorizationPending) || errors.Is(err, oauth.ErrSlowDown) {
			code := "authorization_pending"
			if errors.Is(err, oauth.ErrSlowDown) {
				code = "slow_down"
			}
			writeJSON(w, http.StatusAccepted, map[string]any{
				"status":           "pending",
				"error":            code,
				"interval_seconds": 5,
				"expires_at":       session.ExpiresAt,
			})
			return
		}
		oauthAPIError(w, err)
		return
	}
	if credential == nil || credential.Service != service {
		WriteError(w, http.StatusBadGateway, "invalid OAuth credential")
		return
	}
	id, err := ctx.OAuthResults().Put(OAuthResult{SubjectID: p.User.ID, Service: service, Credential: *credential})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "could not store OAuth result")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "complete", "result_id": id})
}

func (m *OAuthFlowModule) result(ctx ModuleContext, w http.ResponseWriter, r *http.Request, id string) {
	p, ok := PrincipalFromContext(r.Context())
	if !ok || p.User == nil {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	result, err := ctx.OAuthResults().Take(id, p.User.ID)
	if err != nil {
		WriteError(w, http.StatusNotFound, "oauth result not found")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"result": result.Credential})
}

func (m *OAuthFlowModule) callback(ctx ModuleContext, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	q := r.URL.Query()
	state := q.Get("state")
	if state == "" {
		oauthCallbackPage(w, false)
		return
	}
	session, err := ctx.OAuth().SessionForState(state)
	if err != nil {
		oauthCallbackPage(w, false)
		return
	}
	if q.Get("error") != "" {
		_ = ctx.OAuth().DiscardSession(session.ID)
		oauthCallbackPage(w, false)
		return
	}
	code := q.Get("code")
	if code == "" {
		oauthCallbackPage(w, false)
		return
	}
	credential, err := ctx.OAuth().Complete(r.Context(), session.ID, code, state)
	if err != nil || credential == nil || credential.AccessToken == "" {
		oauthCallbackPage(w, false)
		return
	}
	id, err := ctx.OAuthResults().Put(OAuthResult{SubjectID: session.SubjectID, Service: session.Service, Credential: *credential})
	if err != nil {
		oauthCallbackPage(w, false)
		return
	}
	// The page intentionally does not render the ID. The header is useful for
	// programmatic same-origin callback clients without putting it in HTML.
	w.Header().Set("X-OAuth-Result-ID", id)
	oauthCallbackPage(w, true)
}

func callbackURL(cfg Config, r *http.Request, service string) string {
	base := strings.TrimRight(cfg.OAuthCallbackBaseURL, "/")
	if base == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	path := "/callback"
	if service == oauth.OAuthServiceCodex {
		path = "/auth/callback"
	}
	return base + path
}

func validOAuthService(service string) bool {
	return service == oauth.OAuthServiceGrok || service == oauth.OAuthServiceCodex || service == oauth.OAuthServiceClaude || service == oauth.OAuthServiceDummy
}

func oauthAPIError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, oauth.ErrSessionNotFound), errors.Is(err, oauth.ErrSessionExpired):
		WriteError(w, http.StatusBadRequest, "oauth session not found")
	case errors.Is(err, oauth.ErrStateMismatch):
		WriteError(w, http.StatusBadRequest, "invalid OAuth state")
	case errors.Is(err, oauth.ErrUnsupportedFlow):
		WriteError(w, http.StatusBadRequest, "unsupported OAuth flow")
	default:
		WriteError(w, http.StatusBadGateway, "OAuth upstream request failed")
	}
}

func oauthCallbackPage(w http.ResponseWriter, success bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	message := "授权失败，请重新开始授权"
	if success {
		message = "授权成功，可以关闭此窗口"
	}
	_, _ = fmt.Fprintf(w, "<!doctype html><html lang=\"zh-CN\"><meta charset=\"utf-8\"><title>OAuth</title><body>%s</body></html>", template.HTMLEscapeString(message))
}

func methodNotAllowed(w http.ResponseWriter) {
	w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
	WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
