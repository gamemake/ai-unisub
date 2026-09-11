package service

import (
	"ai-unisub/internal/common"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
)

// The browser application is deliberately embedded in the binary.  Static
// handlers must not read files from the process working directory.
//
//go:embed static/login.html static/home.html static/common.css static/home-data.js static/home.js static/login.js
var staticFiles embed.FS

// StaticModule serves the browser entry points and manages the Web Session
// used by the browser application. It does not implement any JSON API other
// than the login operation needed to establish that session.
type StaticModule struct {
	files http.Handler
}

func NewStaticModule() *StaticModule { return &StaticModule{} }
func (m *StaticModule) Name() string { return "static" }
func (m *StaticModule) Close() error { return nil }

func (m *StaticModule) Init(ctx ModuleContext) error {
	root, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return err
	}
	m.files = http.StripPrefix("/static/", http.FileServer(http.FS(root)))

	ctx.HandleFunc("/static/", RouteOptions{Auth: AuthNone, Name: "static.assets"}, m.assets)
	ctx.HandleFunc("/", RouteOptions{Auth: AuthNone, Name: "static.root"}, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		m.redirectBySession(ctx, w, r)
	})
	ctx.HandleFunc("/login", RouteOptions{Auth: AuthNone, Name: "static.login"}, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if _, ok := ctx.Auth().SessionPrincipal(r); ok {
				http.Redirect(w, r, "/home", http.StatusSeeOther)
				return
			}
			m.page(w, r, "login.html")
		case http.MethodPost:
			m.login(ctx, w, r)
		default:
			methodNotAllowed(w)
		}
	})
	ctx.HandleFunc("/api/login", RouteOptions{Auth: AuthNone, Name: "static.login"}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		m.login(ctx, w, r)
	})
	ctx.HandleFunc("/home", RouteOptions{Auth: AuthNone, Name: "static.home"}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		if _, ok := ctx.Auth().SessionPrincipal(r); !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		m.page(w, r, "home.html")
	})
	ctx.HandleFunc("/logout", RouteOptions{Auth: AuthNone, Name: "static.logout"}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		if cookie, err := r.Cookie("session"); err == nil {
			ctx.Auth().ClearSessionCookie(w, cookie.Value)
		} else {
			ctx.Auth().ClearSessionCookie(w, "")
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})
	return nil
}

func (m *StaticModule) assets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	if r.URL.Path == "/static/" || strings.Contains(r.URL.Path, "..") {
		http.NotFound(w, r)
		return
	}
	noStore(w)
	m.files.ServeHTTP(w, r)
}

func (m *StaticModule) page(w http.ResponseWriter, r *http.Request, name string) {
	noStore(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data, _ := staticFiles.ReadFile("static/" + name)
	_, _ = w.Write(data)
}

func (m *StaticModule) redirectBySession(ctx ModuleContext, w http.ResponseWriter, r *http.Request) {
	if _, ok := ctx.Auth().SessionPrincipal(r); ok {
		http.Redirect(w, r, "/home", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (m *StaticModule) login(ctx ModuleContext, w http.ResponseWriter, r *http.Request) {
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
	for i := range users {
		if users[i].Name == input.Username && users[i].Enabled && verifyPassword(users[i].PasswordHash, input.Password) {
			token, err := ctx.Auth().CreateSession(&users[i])
			if err != nil {
				common.WriteError(w, http.StatusInternalServerError, common.MessageCouldNotCreateSession)
				return
			}
			ctx.Auth().SetSessionCookie(w, token)
			writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "user": map[string]any{"id": users[i].ID, "name": users[i].Name, "role": users[i].Role}})
			return
		}
	}
	common.WriteError(w, http.StatusUnauthorized, common.MessageInvalidUsernameOrPassword)
}

func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}
