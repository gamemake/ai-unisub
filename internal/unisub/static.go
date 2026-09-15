package unisub

import (
	"ai-unisub/internal/common"
	"ai-unisub/internal/service"
	"ai-unisub/internal/web"
	"encoding/json"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// StaticModule owns browser routes; the filesystem is injected by the app.
type StaticModule struct{ files fs.FS }

func NewStaticModule(files ...fs.FS) *StaticModule {
	root := web.Files()
	if len(files) > 0 {
		root = files[0]
	}
	return &StaticModule{files: root}
}
func (m *StaticModule) Name() string { return "static" }
func (m *StaticModule) Close() error { return nil }
func (m *StaticModule) Init(ctx service.ModuleContext) error {
	if _, err := fs.Stat(m.files, "index.html"); err != nil {
		return err
	}
	public := service.RouteOptions{Auth: service.AuthNone, Name: "static"}
	ctx.HandleFunc("/", public, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			methodNotAllowed(w)
			return
		}
		if r.URL.Path == "/" {
			m.redirect(ctx, w, r)
			return
		}
		m.asset(w, r)
	})
	ctx.HandleFunc("/login", public, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			m.login(ctx, w, r)
		case http.MethodGet, http.MethodHead:
			if _, ok := ctx.Auth().SessionPrincipal(r); ok {
				http.Redirect(w, r, "/home", http.StatusSeeOther)
				return
			}
			m.page(w, r)
		default:
			methodNotAllowed(w)
		}
	})
	ctx.HandleFunc("/api/login", public, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		m.login(ctx, w, r)
	})
	ctx.HandleFunc("/home", public, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			methodNotAllowed(w)
			return
		}
		if _, ok := ctx.Auth().SessionPrincipal(r); !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		m.page(w, r)
	})
	ctx.HandleFunc("/logout", public, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		token := ""
		if cookie, err := r.Cookie("session"); err == nil {
			token = cookie.Value
		}
		ctx.Auth().ClearSessionCookie(w, token)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})
	return nil
}
func (m *StaticModule) redirect(ctx service.ModuleContext, w http.ResponseWriter, r *http.Request) {
	target := "/login"
	if _, ok := ctx.Auth().SessionPrincipal(r); ok {
		target = "/home"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
func (m *StaticModule) page(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(m.files, "index.html")
	if err != nil {
		common.WriteError(w, http.StatusServiceUnavailable, common.MessageWebBuildUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}
func (m *StaticModule) asset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	// Do not turn unknown API paths into HTML, list directories, or expose dotfiles.
	if !fs.ValidPath(name) || strings.Contains(name, "\\") {
		http.NotFound(w, r)
		return
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") {
			http.NotFound(w, r)
			return
		}
	}
	if path.Ext(name) == ".html" {
		http.NotFound(w, r)
		return
	}
	info, err := fs.Stat(m.files, name)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.FileServerFS(m.files).ServeHTTP(w, r)
}
func (m *StaticModule) login(ctx service.ModuleContext, w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input) != nil || strings.TrimSpace(input.Username) == "" || input.Password == "" {
		common.WriteError(w, http.StatusBadRequest, common.MessageUsernamePasswordRequired)
		return
	}
	users, err := ctx.Database().ListUsers()
	if err != nil {
		common.WriteError(w, http.StatusInternalServerError, common.MessageCouldNotAuthenticateUser)
		return
	}
	for _, user := range users {
		if user.Name != input.Username || !user.Enabled || !service.VerifyPassword(user.PasswordHash, input.Password) {
			continue
		}
		token, err := ctx.Auth().CreateSession(&user)
		if err != nil {
			common.WriteError(w, http.StatusInternalServerError, common.MessageCouldNotAuthenticateUser)
			return
		}
		ctx.Auth().SetSessionCookie(w, token)
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "user": publicUser(user)})
		return
	}
	common.WriteError(w, http.StatusUnauthorized, common.MessageInvalidUsernameOrPassword)
}
