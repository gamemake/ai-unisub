package unisub

import (
	"ai-unisub/internal/common"
	"ai-unisub/internal/oauth"
	"ai-unisub/internal/proxy"
	framework "ai-unisub/internal/service"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// OAuthFlowModule exposes the Web OAuth API and upstream callback endpoints.
// The OAuth protocol itself remains implemented by internal/oauth adapters.
type OAuthFlowModule struct{}

func NewOAuthFlowModule() *OAuthFlowModule { return &OAuthFlowModule{} }
func (m *OAuthFlowModule) Name() string    { return "oauthflow" }
func (m *OAuthFlowModule) Close() error    { return nil }

func (m *OAuthFlowModule) Init(ctx framework.ModuleContext) error {
	ctx.HandleFunc("/api/oauth/", framework.RouteOptions{Auth: framework.AuthSession, Name: "oauth-api"}, func(w http.ResponseWriter, r *http.Request) {
		m.api(ctx, w, r)
	})
	for _, path := range []string{"/auth/callback", "/callback", "/oauth/code/callback"} {
		ctx.HandleFunc(path, framework.RouteOptions{Auth: framework.AuthNone, Name: "oauth-callback"}, func(w http.ResponseWriter, r *http.Request) {
			m.callback(ctx, w, r)
		})
	}
	return nil
}

func (m *OAuthFlowModule) api(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		common.WriteError(w, http.StatusForbidden, common.MessageForbidden)
		return
	}
	parts := strings.Split(strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, "/"), "/api/oauth/"), "/")
	if len(parts) == 3 && parts[1] == "status" && r.Method == http.MethodGet {
		principal, ok := framework.PrincipalFromContext(r.Context())
		if !ok || principal.User == nil {
			common.WriteError(w, 401, common.MessageUnauthorized)
			return
		}
		if id, found := ctx.OAuthResults().FindSession(parts[2], parts[0], strconv.Itoa(principal.User.ID)); found {
			writeJSON(w, 200, map[string]string{"status": "complete", "result_id": id})
			return
		}
		if _, err := ctx.OAuth().SessionForSubject(parts[2], parts[0], strconv.Itoa(principal.User.ID)); err != nil {
			oauthAPIError(w, err)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "pending"})
		return
	}
	if len(parts) == 3 && parts[1] == "complete" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		m.complete(ctx, w, r, parts[0], parts[2])
		return
	}
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

// complete accepts an authorization code from the signed-in session owner.
// Credential results remain authenticated, short-lived and one-time readable.
func (m *OAuthFlowModule) complete(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request, name, sessionID string) {
	principal, ok := framework.PrincipalFromContext(r.Context())
	if !ok || principal.User == nil {
		common.WriteError(w, 401, common.MessageUnauthorized)
		return
	}
	if !validOAuthService(name) {
		common.WriteError(w, 400, common.MessageUnsupportedOAuthService)
		return
	}
	if _, err := ctx.OAuth().SessionForSubject(sessionID, name, strconv.Itoa(principal.User.ID)); err != nil {
		oauthAPIError(w, err)
		return
	}
	var input struct {
		Code  string `json:"code"`
		State string `json:"state"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Code == "" || input.State == "" {
		common.WriteError(w, 400, common.MessageInvalidOAuthState)
		return
	}
	credential, err := ctx.OAuth().Complete(r.Context(), sessionID, input.Code, input.State)
	if err != nil {
		oauthAPIError(w, err)
		return
	}
	if credential == nil || credential.AccessToken == "" {
		common.WriteError(w, 502, common.MessageInvalidOAuthCredential)
		return
	}
	id, err := ctx.OAuthResults().Put(framework.OAuthResult{SessionID: sessionID, SubjectID: strconv.Itoa(principal.User.ID), Service: name, Credential: *credential})
	if err != nil {
		common.WriteError(w, 500, common.MessageCouldNotStoreOAuthResult)
		return
	}
	writeJSON(w, 200, map[string]string{"result_id": id})
}

func (m *OAuthFlowModule) start(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request, service string) {
	p, ok := framework.PrincipalFromContext(r.Context())
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
		Proxy        string `json:"proxy"`
		ProxyGroupID int    `json:"proxy_group_id"`
	}
	if err := decodeOptionalJSON(r, &input); err != nil {
		common.WriteError(w, http.StatusBadRequest, common.MessageInvalidJSONBody)
		return
	}
	reqCtx := r.Context()
	var endpoint *proxy.Endpoint
	if input.ProxyGroupID != 0 {
		if input.Proxy != "" {
			common.WriteError(w, 400, "choose proxy_group_id or proxy, not both")
			return
		}
		var err error
		endpoint, err = ctx.Proxy().ResolveProxy(reqCtx, input.ProxyGroupID, service, nil)
		if err != nil {
			common.WriteError(w, 503, common.MessageInvalidProxy)
			return
		}
		// Authorization may be interactive and outlive a probe lease. Selection
		// does not prove health; release admission without reporting success.
		defer func() { _ = ctx.Proxy().ReportProxy(endpoint, service, proxy.Canceled) }()
	}
	if strings.TrimSpace(input.Proxy) != "" {
		var err error
		endpoint, err = proxy.NewEndpoint(input.Proxy)
		if err != nil {
			common.WriteError(w, http.StatusBadRequest, common.MessageInvalidProxy)
			return
		}
	}

	var client *http.Client
	if endpoint != nil {
		client = proxy.Client(http.DefaultClient, endpoint)
	}
	result, err := ctx.OAuth().Start(reqCtx, service, strconv.Itoa(p.User.ID), redirect, client)
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

func (m *OAuthFlowModule) poll(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request, service, sessionID string) {
	p, ok := framework.PrincipalFromContext(r.Context())
	if !ok || p.User == nil {
		common.WriteError(w, http.StatusUnauthorized, common.MessageUnauthorized)
		return
	}
	if !validOAuthService(service) || sessionID == "" {
		common.WriteError(w, http.StatusBadRequest, common.MessageInvalidOAuthSession)
		return
	}
	session, err := ctx.OAuth().SessionForSubject(sessionID, service, strconv.Itoa(p.User.ID))
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
	id, err := ctx.OAuthResults().Put(framework.OAuthResult{SubjectID: strconv.Itoa(p.User.ID), Service: service, Credential: *credential})
	if err != nil {
		common.WriteError(w, http.StatusInternalServerError, common.MessageCouldNotStoreOAuthResult)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "complete", "result_id": id})
}

func (m *OAuthFlowModule) result(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request, id string) {
	p, ok := framework.PrincipalFromContext(r.Context())
	if !ok || p.User == nil {
		common.WriteError(w, http.StatusUnauthorized, common.MessageUnauthorized)
		return
	}
	result, err := ctx.OAuthResults().Take(id, strconv.Itoa(p.User.ID))
	if err != nil {
		common.WriteError(w, http.StatusNotFound, common.MessageOAuthResultNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"result": result.Credential})
}

func (m *OAuthFlowModule) callback(ctx framework.ModuleContext, w http.ResponseWriter, r *http.Request) {
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
	id, err := ctx.OAuthResults().Put(framework.OAuthResult{SessionID: session.ID, SubjectID: session.SubjectID, Service: session.Service, Credential: *credential})
	if err != nil {
		oauthCallbackPage(w, false)
		return
	}
	// The page intentionally does not render the ID. The header is useful for
	// programmatic same-origin callback clients without putting it in HTML.
	w.Header().Set("X-OAuth-Result-ID", id)
	oauthCallbackPage(w, true)
}

func callbackURL(cfg framework.Config, r *http.Request, service string) string {
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
