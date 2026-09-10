package service

import (
	"ai-unisub2/internal/database"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newStaticTestService(t *testing.T) (*Service, string) {
	t.Helper()
	db := database.NewMemoryDatabase()
	user := &database.PersistedUser{ID: "admin-id", Name: "admin", Role: database.UserRoleAdmin, PasswordHash: hashPassword("password-123")}
	if err := db.SaveUser(user); err != nil {
		t.Fatal(err)
	}
	s, err := NewWithDependencies(Config{DatabaseURL: "sqlite::memory:"}, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddModule(NewStaticModule()); err != nil {
		s.Close()
		t.Fatal(err)
	}
	return s, user.ID
}

func TestStaticModulePagesAssetsAndRedirects(t *testing.T) {
	s, _ := newStaticTestService(t)
	defer s.Close()

	get := func(path string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if got := get("/", nil); got.Code != http.StatusSeeOther || got.Header().Get("Location") != "/login" {
		t.Fatalf("anonymous root: status=%d location=%q", got.Code, got.Header().Get("Location"))
	}
	if got := get("/home", nil); got.Code != http.StatusSeeOther || got.Header().Get("Location") != "/login" {
		t.Fatalf("anonymous home: status=%d location=%q", got.Code, got.Header().Get("Location"))
	}
	if got := get("/login", nil); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "loginForm") {
		t.Fatalf("login page: status=%d body=%q", got.Code, got.Body.String()[:min(80, got.Body.Len())])
	}
	if got := get("/static/admin.css", nil); got.Code != http.StatusOK || got.Header().Get("Content-Type") == "" {
		t.Fatalf("static css: status=%d content-type=%q", got.Code, got.Header().Get("Content-Type"))
	}
	if got := get("/static/../admin.css", nil); got.Code != http.StatusNotFound {
		t.Fatalf("path traversal: status=%d", got.Code)
	}
}

func TestStaticModuleLoginAndLogout(t *testing.T) {
	s, _ := newStaticTestService(t)
	defer s.Close()

	login := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{"username":"admin","password":"password-123"}`))
	login.Header.Set("Content-Type", "application/json")
	loginWriter := httptest.NewRecorder()
	s.Handler().ServeHTTP(loginWriter, login)
	if loginWriter.Code != http.StatusOK {
		t.Fatalf("login: status=%d body=%s", loginWriter.Code, loginWriter.Body.String())
	}
	cookies := loginWriter.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "session" || cookies[0].Value == "" {
		t.Fatalf("login cookie: %#v", cookies)
	}

	home := httptest.NewRequest(http.MethodGet, "/home", nil)
	home.AddCookie(cookies[0])
	homeWriter := httptest.NewRecorder()
	s.Handler().ServeHTTP(homeWriter, home)
	if homeWriter.Code != http.StatusOK {
		t.Fatalf("authenticated home: status=%d location=%q", homeWriter.Code, homeWriter.Header().Get("Location"))
	}
	if !strings.Contains(homeWriter.Body.String(), `value="dummy"`) {
		t.Fatal("home page does not expose dummy provider option")
	}

	logout := httptest.NewRequest(http.MethodPost, "/logout", nil)
	logout.AddCookie(cookies[0])
	logoutWriter := httptest.NewRecorder()
	s.Handler().ServeHTTP(logoutWriter, logout)
	if logoutWriter.Code != http.StatusSeeOther || logoutWriter.Header().Get("Location") != "/login" {
		t.Fatalf("logout: status=%d location=%q", logoutWriter.Code, logoutWriter.Header().Get("Location"))
	}
	if got := func() int {
		r := httptest.NewRequest(http.MethodGet, "/home", nil)
		r.AddCookie(cookies[0])
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w.Code
	}(); got != http.StatusSeeOther {
		t.Fatalf("home after logout: status=%d", got)
	}
}
