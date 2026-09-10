package service

import (
	"ai-unisub2/internal/database"
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
	key := request(http.MethodPost, "/api/keys", `{"account_id":"account-1","valid_seconds":86400}`)
	if key.Code != http.StatusCreated || !strings.Contains(key.Body.String(), `"key"`) || !strings.Contains(key.Body.String(), `"valid_seconds":86400`) {
		t.Fatalf("create key: status=%d body=%s", key.Code, key.Body.String())
	}
	if got := request(http.MethodPost, "/api/keys", `{"account_id":"account-1","valid_seconds":-1}`); got.Code != http.StatusBadRequest {
		t.Fatalf("negative key validity: status=%d body=%s", got.Code, got.Body.String())
	}
	listed := request(http.MethodGet, "/api/keys", "")
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), `"key"`) {
		t.Fatalf("list keys exposed secret: status=%d body=%s", listed.Code, listed.Body.String())
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
