package service

import (
	"ai-unisub/internal/database"
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
	if got := get("/login", nil); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `id="apiKeyName"`) {
		t.Fatalf("login page missing API key name field")
	}
	loginHTML := get("/login", nil).Body.String()
	if !strings.Contains(loginHTML, `id="serverVersion"`) || !strings.Contains(loginHTML, `id="serverCopyright"`) {
		t.Fatal("sidebar footer should show server metadata")
	}
	topbarStart := strings.Index(loginHTML, `<header class="topbar">`)
	topbarEnd := -1
	if topbarStart >= 0 {
		topbarEnd = strings.Index(loginHTML[topbarStart:], `</header>`)
	}
	if topbarStart < 0 || topbarEnd < 0 {
		t.Fatal("top bar is missing")
	}
	topbarHTML := loginHTML[topbarStart : topbarStart+topbarEnd]
	if !strings.Contains(topbarHTML, `id="logoutButton"`) || !strings.Contains(topbarHTML, `class="btn btn-ghost icon-btn"`) || !strings.Contains(topbarHTML, `aria-label="退出登录"`) {
		t.Fatal("top bar should contain an accessible icon-only logout button")
	}
	sidebarEnd := strings.Index(loginHTML, `</aside>`)
	if sidebarEnd < 0 || strings.Contains(loginHTML[:sidebarEnd], `id="logoutButton"`) {
		t.Fatal("sidebar should not contain the logout button")
	}
	if strings.Contains(loginHTML, `id="sidebarUserName"`) || strings.Contains(loginHTML, `id="sidebarUserRole"`) || strings.Contains(loginHTML, `id="sidebarUserAvatar"`) {
		t.Fatal("sidebar footer should not show current user information")
	}
	keyModalStart := strings.Index(loginHTML, `id="keyModal"`)
	if keyModalStart < 0 {
		t.Fatal("API key result modal is missing")
	}
	keyModalEnd := strings.Index(loginHTML[keyModalStart:], `id="userModal"`)
	if keyModalEnd < 0 {
		t.Fatal("API key result modal boundary is missing")
	}
	keyModalHTML := loginHTML[keyModalStart : keyModalStart+keyModalEnd]
	if strings.Contains(keyModalHTML, "我已保存") || !strings.Contains(keyModalHTML, ">关闭</button>") {
		t.Fatal("API key result modal should use Close instead of Saved")
	}
	if strings.Count(keyModalHTML, "btn-primary") != 1 {
		t.Fatal("API key result modal should have exactly one primary button")
	}
	if !strings.Contains(keyModalHTML, `class="key-export-actions"`) || !strings.Contains(keyModalHTML, `id="copyKeyButton"`) || !strings.Contains(keyModalHTML, `id="ccSwitchFromKeyModal"`) {
		t.Fatal("API key result modal should place Copy and CC Switch in one action row")
	}
	if !strings.Contains(loginHTML, "排队超时") || !strings.Contains(loginHTML, "同时转发的最大请求数") || !strings.Contains(loginHTML, `name="accountEnabled"`) {
		t.Fatal("account form missing queue timeout, concurrency hint, or status radios")
	}
	if strings.Contains(loginHTML, "id=\"credentialHint\"") || strings.Contains(loginHTML, "id=\"clearProxy\"") {
		t.Fatal("account form still has removed credential hint or clear-proxy control")
	}
	if got := get("/static/admin.js", nil); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "copyAPIKey") || !strings.Contains(got.Body.String(), "launchCCSwitch") || !strings.Contains(got.Body.String(), "keySecret") || !strings.Contains(got.Body.String(), ">名称</th>") || !strings.Contains(got.Body.String(), ">过期时间</th>") {
		t.Fatalf("admin js missing API key name/copy/CC Switch/expiry UI")
	}
	if js := get("/static/admin.js", nil).Body.String(); strings.Contains(js, "var id=Number(button.dataset.id)") {
		t.Fatal("account action still coerces hex IDs to numbers")
	}
	if js := get("/static/admin.js", nil).Body.String(); !strings.Contains(js, "setAuthJSON") || strings.Contains(js, "credentialSummaryHTML") {
		t.Fatal("admin js should display OAuthCredential JSON as-is")
	}
	if js := get("/static/admin.js", nil).Body.String(); !strings.Contains(js, "window.location.assign('/home')") || !strings.Contains(js, "function renderServerMeta") {
		t.Fatal("admin js should navigate to /home after login and render server metadata")
	}
	if js := get("/static/admin.js", nil).Body.String(); !strings.Contains(js, "function parseResponse") || !strings.Contains(js, "function errorText") || !strings.Contains(js, "body.error") {
		t.Fatal("admin js should parse and translate JSON error responses centrally")
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
