package unisub

import (
	"ai-unisub/internal/common"
	"ai-unisub/internal/service"
	"ai-unisub/internal/web"
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
		if r.URL.Path == "/login" || r.URL.Path == "/home" || r.URL.Path == "/logout" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			methodNotAllowed(w)
			return
		}
		if r.URL.Path == "/" {
			m.page(w, r)
			return
		}
		m.asset(w, r)
	})
	return nil
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
