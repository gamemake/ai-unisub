package service

import (
	"ai-unisub/internal/database"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIModuleUserPasswordAndKeyLifecycle(t *testing.T) {
	db := database.NewMemoryDatabase()
	admin := &database.PersistedUser{ID: "admin", Name: "admin", Role: database.UserRoleAdmin, PasswordHash: hashPassword("old-password")}
	if err := db.SaveUser(admin); err != nil {
		t.Fatal(err)
	}
	s, err := NewWithDependencies(Config{DatabaseURL: "sqlite::memory:"}, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AddModule(NewAPIModule()); err != nil {
		t.Fatal(err)
	}
	token, err := s.Auth().CreateSession(admin)
	if err != nil {
		t.Fatal(err)
	}

	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	me := request(http.MethodGet, "/api/me", "")
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"server_version":"`+Version+`"`) {
		t.Fatalf("current user version: status=%d body=%s", me.Code, me.Body.String())
	}

	created := request(http.MethodPost, "/api/users", `{"name":"user","password":"new-password","role":"user"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create user: status=%d body=%s", created.Code, created.Body.String())
	}

	account := &database.PersistedAccount{ID: "account-1", Provider: "claude", Name: "Claude", Config: json.RawMessage(`{"credential_id":"credential-1"}`)}
	if err := db.SaveAccount(account); err != nil {
		t.Fatal(err)
	}
	updated := request(http.MethodPut, "/api/providers/account-1", `{"name":"Claude updated","provider":"claude","config":{}}`)
	if updated.Code != http.StatusOK {
		t.Fatalf("update provider: status=%d body=%s", updated.Code, updated.Body.String())
	}
	accounts, err := db.ListAccounts()
	if err != nil || len(accounts) != 1 || accounts[0].Name != "Claude updated" {
		t.Fatalf("provider was not updated: accounts=%+v err=%v", accounts, err)
	}
	if got := request(http.MethodPost, "/api/keys", `{"account_id":"account-1","valid_seconds":86400}`); got.Code != http.StatusBadRequest {
		t.Fatalf("missing key name: status=%d body=%s", got.Code, got.Body.String())
	}
	key := request(http.MethodPost, "/api/keys", `{"name":"claude-code","account_id":"account-1","valid_seconds":86400}`)
	if key.Code != http.StatusCreated || !strings.Contains(key.Body.String(), `"key"`) || !strings.Contains(key.Body.String(), `"name":"claude-code"`) || !strings.Contains(key.Body.String(), `"valid_seconds":86400`) || !strings.Contains(key.Body.String(), `"expires_at"`) {
		t.Fatalf("create key: status=%d body=%s", key.Code, key.Body.String())
	}
	var createdKey map[string]any
	if err := json.Unmarshal(key.Body.Bytes(), &createdKey); err != nil {
		t.Fatalf("decode created key: %v", err)
	}
	keyID, _ := createdKey["id"].(string)
	secret, _ := createdKey["key"].(string)
	if keyID == "" || secret == "" {
		t.Fatalf("created key missing id or secret: %s", key.Body.String())
	}
	if got := request(http.MethodPost, "/api/keys", `{"name":"claude-code","account_id":"account-1","valid_seconds":-1}`); got.Code != http.StatusBadRequest {
		t.Fatalf("negative key validity: status=%d body=%s", got.Code, got.Body.String())
	}
	listed := request(http.MethodGet, "/api/keys", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"key":"`+secret+`"`) || !strings.Contains(listed.Body.String(), `"name":"claude-code"`) || !strings.Contains(listed.Body.String(), `"expires_at"`) {
		t.Fatalf("list keys: status=%d body=%s", listed.Code, listed.Body.String())
	}
	fetched := request(http.MethodGet, "/api/keys/"+keyID, "")
	if fetched.Code != http.StatusOK || !strings.Contains(fetched.Body.String(), `"key":"`+secret+`"`) || !strings.Contains(fetched.Body.String(), `"name":"claude-code"`) {
		t.Fatalf("get key: status=%d body=%s", fetched.Code, fetched.Body.String())
	}

	changed := request(http.MethodPost, "/api/password", `{"old_password":"old-password","new_password":"changed-password"}`)
	if changed.Code != http.StatusOK {
		t.Fatalf("change password: status=%d body=%s", changed.Code, changed.Body.String())
	}
	if got := request(http.MethodGet, "/api/calls?page_size=10", ""); got.Code != http.StatusBadRequest {
		t.Fatalf("invalid call page size: status=%d body=%s", got.Code, got.Body.String())
	}
	if got := request(http.MethodGet, "/api/calls?page_size=20", ""); got.Code != http.StatusOK {
		t.Fatalf("valid call page size: status=%d body=%s", got.Code, got.Body.String())
	}
}

func TestAPIModuleProviderEnabledProxyAndConcurrency(t *testing.T) {
	db := database.NewMemoryDatabase()
	admin := &database.PersistedUser{ID: "admin", Name: "admin", Role: database.UserRoleAdmin, PasswordHash: hashPassword("password")}
	if err := db.SaveUser(admin); err != nil {
		t.Fatal(err)
	}
	s, err := NewWithDependencies(Config{DatabaseURL: "sqlite::memory:"}, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AddModule(NewAPIModule()); err != nil {
		t.Fatal(err)
	}
	token, err := s.Auth().CreateSession(admin)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}

	if got := request(http.MethodPost, "/api/providers", `{"name":"Dummy","provider":"dummy","config":{"proxy":"ftp://127.0.0.1:21"}}`); got.Code != http.StatusBadRequest {
		t.Fatalf("invalid proxy: status=%d body=%s", got.Code, got.Body.String())
	}
	created := request(http.MethodPost, "/api/providers", `{"name":"Dummy","provider":"dummy","config":{"enabled":false,"proxy":"http://127.0.0.1:8080","max_concurrent_connections":4,"queue_timeout_seconds":15}}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create provider: status=%d body=%s", created.Code, created.Body.String())
	}
	var createdBody map[string]any
	if err := json.Unmarshal(created.Body.Bytes(), &createdBody); err != nil {
		t.Fatal(err)
	}
	if createdBody["enabled"] != false {
		t.Fatalf("created enabled=%v body=%s", createdBody["enabled"], created.Body.String())
	}
	id, _ := createdBody["id"].(string)
	listed := request(http.MethodGet, "/api/providers", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"enabled":false`) || !strings.Contains(listed.Body.String(), `"proxy":"http://127.0.0.1:8080"`) {
		t.Fatalf("list provider: status=%d body=%s", listed.Code, listed.Body.String())
	}
	if err := db.SaveCredential("cred-1", json.RawMessage(`{"service":"dummy","access_token":"secret-token","refresh_token":"secret-refresh","email":"dummy@example.test"}`)); err != nil {
		t.Fatal(err)
	}
	withCred := request(http.MethodPut, "/api/providers/"+id, `{"name":"Dummy","provider":"dummy","config":{"credential_id":"cred-1","enabled":false,"proxy":"http://127.0.0.1:8080"}}`)
	if withCred.Code != http.StatusOK {
		t.Fatalf("attach credential: status=%d body=%s", withCred.Code, withCred.Body.String())
	}
	listed = request(http.MethodGet, "/api/providers", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"access_token":"secret-token"`) || !strings.Contains(listed.Body.String(), `"email":"dummy@example.test"`) {
		t.Fatalf("list missing OAuthCredential: status=%d body=%s", listed.Code, listed.Body.String())
	}
	updated := request(http.MethodPut, "/api/providers/"+id, `{"name":"Dummy","provider":"dummy","config":{"enabled":true,"proxy":"socks5://127.0.0.1:1080","max_concurrent_connections":2,"queue_timeout_seconds":30}}`)
	if updated.Code != http.StatusOK {
		t.Fatalf("update provider: status=%d body=%s", updated.Code, updated.Body.String())
	}
	listed = request(http.MethodGet, "/api/providers", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"enabled":true`) || !strings.Contains(listed.Body.String(), `"proxy":"socks5://127.0.0.1:1080"`) || !strings.Contains(listed.Body.String(), `"max_concurrent_connections":2`) {
		t.Fatalf("updated list: status=%d body=%s", listed.Code, listed.Body.String())
	}
	p, ok := s.Providers().Get(id)
	if !ok {
		t.Fatal("runtime provider missing")
	}
	if !p.Config().Enabled || p.Config().Proxy != "socks5://127.0.0.1:1080" || p.Config().MaxConcurrentConnections != 2 || p.Config().QueueTimeoutSeconds != 30 {
		t.Fatalf("runtime config: %+v", p.Config())
	}
}

func TestAPIModuleRejectsNonAdminManagement(t *testing.T) {
	db := database.NewMemoryDatabase()
	user := &database.PersistedUser{ID: "user", Name: "user", Role: database.UserRoleUser, PasswordHash: hashPassword("password")}
	if err := db.SaveUser(user); err != nil {
		t.Fatal(err)
	}
	s, err := NewWithDependencies(Config{DatabaseURL: "sqlite::memory:"}, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AddModule(NewAPIModule()); err != nil {
		t.Fatal(err)
	}
	token, err := s.Auth().CreateSession(user)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	r.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-admin users: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAPIModuleAdminUserControls(t *testing.T) {
	db := database.NewMemoryDatabase()
	admin := &database.PersistedUser{ID: "admin", Name: "admin", Role: database.UserRoleAdmin, PasswordHash: hashPassword("admin-password")}
	other := &database.PersistedUser{ID: "other", Name: "other", Role: database.UserRoleUser, PasswordHash: hashPassword("old-password")}
	if err := db.SaveUser(admin); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveUser(other); err != nil {
		t.Fatal(err)
	}
	s, err := NewWithDependencies(Config{DatabaseURL: "sqlite::memory:"}, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AddModule(NewAPIModule()); err != nil {
		t.Fatal(err)
	}
	token, err := s.Auth().CreateSession(admin)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if got := request(http.MethodPost, "/api/users/other/password", `{"password":"new-password"}`); got.Code != http.StatusOK {
		t.Fatalf("reset password: status=%d body=%s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPut, "/api/users/other", `{"enabled":false}`); got.Code != http.StatusOK {
		t.Fatalf("disable user: status=%d body=%s", got.Code, got.Body.String())
	}
	users, err := db.ListUsers()
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range users {
		if user.ID == "other" && user.Enabled {
			t.Fatal("other user was not disabled")
		}
	}
	if got := request(http.MethodPut, "/api/users/admin", `{"role":"user"}`); got.Code != http.StatusBadRequest {
		t.Fatalf("self demotion: status=%d body=%s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPut, "/api/users/admin", `{"enabled":false}`); got.Code != http.StatusBadRequest {
		t.Fatalf("self disable: status=%d body=%s", got.Code, got.Body.String())
	}
	if got := request(http.MethodDelete, "/api/users/admin", ""); got.Code != http.StatusBadRequest {
		t.Fatalf("self delete: status=%d body=%s", got.Code, got.Body.String())
	}
}
