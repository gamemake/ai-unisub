package service

import (
	"ai-unisub/internal/common"
	"ai-unisub/internal/oauth"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
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
		common.WriteError(w, http.StatusUnauthorized, common.MessageUnauthorized)
		return
	}
	if !validOAuthService(service) {
		common.WriteError(w, http.StatusBadRequest, common.MessageUnsupportedOAuthService)
		return
	}
	redirect := callbackURL(ctx.Config(), r, service)
	var input struct {
		Proxy string `json:"proxy"`
	}
	if err := decodeOptionalJSON(r, &input); err != nil {
		common.WriteError(w, http.StatusBadRequest, common.MessageInvalidJSONBody)
		return
	}
	reqCtx := r.Context()
	if proxy := strings.TrimSpace(input.Proxy); proxy != "" {
		if _, err := common.ParseHTTPProxy(proxy); err != nil {
			common.WriteError(w, http.StatusBadRequest, common.MessageInvalidProxy)
			return
		}
		reqCtx = common.WithHTTPProxy(reqCtx, proxy)
	}
	result, err := ctx.OAuth().Start(reqCtx, service, p.User.ID, redirect)
	if err != nil {
		oauthAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func decodeOptionalJSON(r *http.Request, dest any) error {
	if r.Body == nil {
		return nil
	}
	err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(dest)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func (m *OAuthFlowModule) poll(ctx ModuleContext, w http.ResponseWriter, r *http.Request, service, sessionID string) {
	p, ok := PrincipalFromContext(r.Context())
	if !ok || p.User == nil {
		common.WriteError(w, http.StatusUnauthorized, common.MessageUnauthorized)
		return
	}
	if !validOAuthService(service) || sessionID == "" {
		common.WriteError(w, http.StatusBadRequest, common.MessageInvalidOAuthSession)
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
	if credential == nil || credential.AccessToken == "" {
		common.WriteError(w, http.StatusBadGateway, common.MessageInvalidOAuthCredential)
		return
	}
	id, err := ctx.OAuthResults().Put(OAuthResult{SubjectID: p.User.ID, Service: service, Credential: *credential})
	if err != nil {
		common.WriteError(w, http.StatusInternalServerError, common.MessageCouldNotStoreOAuthResult)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "complete", "result_id": id})
}

func (m *OAuthFlowModule) result(ctx ModuleContext, w http.ResponseWriter, r *http.Request, id string) {
	p, ok := PrincipalFromContext(r.Context())
	if !ok || p.User == nil {
		common.WriteError(w, http.StatusUnauthorized, common.MessageUnauthorized)
		return
	}
	result, err := ctx.OAuthResults().Take(id, p.User.ID)
	if err != nil {
		common.WriteError(w, http.StatusNotFound, common.MessageOAuthResultNotFound)
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
		common.WriteError(w, http.StatusBadRequest, common.MessageOAuthSessionNotFound)
	case errors.Is(err, oauth.ErrStateMismatch):
		common.WriteError(w, http.StatusBadRequest, common.MessageInvalidOAuthState)
	case errors.Is(err, oauth.ErrUnsupportedFlow):
		common.WriteError(w, http.StatusBadRequest, common.MessageUnsupportedOAuthFlow)
	default:
		common.WriteError(w, http.StatusBadGateway, common.MessageOAuthUpstreamFailed)
	}
}

func oauthCallbackPage(w http.ResponseWriter, success bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	message := common.MessageOAuthAuthorizationFailed
	if success {
		message = "授权成功，可以关闭此窗口"
	}
	_, _ = fmt.Fprintf(w, "<!doctype html><html lang=\"zh-CN\"><meta charset=\"utf-8\"><title>OAuth</title><body>%s</body></html>", template.HTMLEscapeString(message))
}

func methodNotAllowed(w http.ResponseWriter) {
	w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
	common.WriteError(w, http.StatusMethodNotAllowed, common.MessageMethodNotAllowed)
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
