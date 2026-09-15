package unisub

import (
	"ai-unisub/internal/service"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func testApp(t *testing.T) *service.Service {
	t.Helper()
	srv, err := New(Config{Service: service.Config{DatabaseURL: "sqlite::memory:", AdminUsername: "admin", AdminPassword: "test-password-123"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}
func appRequest(s *service.Service, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	out := httptest.NewRecorder()
	s.Handler().ServeHTTP(out, req)
	return out
}
func loginTestApp(t *testing.T, s *service.Service) *http.Cookie {
	t.Helper()
	out := appRequest(s, "POST", "/api/login", `{"username":"admin","password":"test-password-123"}`, nil)
	if out.Code != 200 {
		t.Fatalf("login: %d %s", out.Code, out.Body.String())
	}
	return out.Result().Cookies()[0]
}
func TestStaticRoutesSessionAndAssets(t *testing.T) {
	s := testApp(t)
	for _, path := range []string{"/", "/home"} {
		out := appRequest(s, "GET", path, "", nil)
		if out.Code != 303 || out.Header().Get("Location") != "/login" {
			t.Fatalf("anonymous %s: %d", path, out.Code)
		}
	}
	out := appRequest(s, "GET", "/login", "", nil)
	if out.Code != 200 || !strings.Contains(out.Body.String(), `id="root"`) || out.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("React entry: %d %s", out.Code, out.Body.String())
	}
	cookie := loginTestApp(t, s)
	if !cookie.HttpOnly {
		t.Fatal("session cookie must be HttpOnly")
	}
	if out := appRequest(s, "GET", "/home", "", cookie); out.Code != 200 {
		t.Fatalf("home: %d", out.Code)
	}
	if out := appRequest(s, "GET", "/login", "", cookie); out.Code != 303 || out.Header().Get("Location") != "/home" {
		t.Fatalf("authenticated login: %d", out.Code)
	}
	if out := appRequest(s, "GET", "/favicon.svg", "", nil); out.Code != 200 || !strings.Contains(out.Header().Get("Content-Type"), "svg") {
		t.Fatalf("favicon: %d", out.Code)
	}
	for _, path := range []string{"/assets/", "/../go.mod", "/.env", "/assets/%2e%2e/go.mod", "/index.html", "/missing.js", "/src/main.tsx"} {
		if out := appRequest(s, "GET", path, "", nil); out.Code != 404 {
			t.Fatalf("unexpected file exposure %s: %d", path, out.Code)
		}
	}
	for _, test := range []struct{ method, path string }{{"POST", "/home"}, {"DELETE", "/login"}, {"POST", "/favicon.svg"}} {
		if out := appRequest(s, test.method, test.path, "", cookie); out.Code != 405 {
			t.Fatalf("method %s %s: %d", test.method, test.path, out.Code)
		}
	}
	if out := appRequest(s, "POST", "/logout", "", cookie); out.Code != 303 {
		t.Fatal("logout failed")
	}
	if out := appRequest(s, "GET", "/api/me", "", cookie); out.Code != 401 {
		t.Fatal("logged out session is still valid")
	}
}
func TestStaticDEVReadsChangedFilesAndPRDDoesNot(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "index.html")
	if err := os.WriteFile(file, []byte("first build"), 0600); err != nil {
		t.Fatal(err)
	}
	dev, err := staticFiles(Config{Mode: "DEV", WebDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	module := NewStaticModule(dev)
	for _, value := range []string{"first build", "updated build"} {
		if err := os.WriteFile(file, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		out := httptest.NewRecorder()
		module.page(out, httptest.NewRequest("GET", "/login", nil))
		if out.Body.String() != value {
			t.Fatal("DEV cached the previous build")
		}
	}
	prod, err := staticFiles(Config{Mode: "PRD", WebDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	content, err := fs.ReadFile(prod, "index.html")
	if err != nil || !strings.Contains(string(content), `id="root"`) {
		t.Fatal("PRD did not use embedded assets")
	}
	if _, err := staticFiles(Config{Mode: "typo"}); err == nil {
		t.Fatal("invalid mode accepted")
	}
	if _, err := staticFiles(Config{Mode: "DEV", WebDir: filepath.Join(dir, "missing")}); err == nil {
		t.Fatal("missing build accepted")
	}
	// Injected filesystems make module tests independent of application globals.
	bad := NewStaticModule(fstest.MapFS{})
	if err := bad.Init(nil); err == nil {
		t.Fatal("module should reject a missing entrypoint")
	}
}
