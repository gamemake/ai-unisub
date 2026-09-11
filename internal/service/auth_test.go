package service

import (
	"ai-unisub/internal/database"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnsureAdminCreatesEnabledUserThatCanLogin(t *testing.T) {
	db := database.NewMemoryDatabase()
	s, err := NewWithDependencies(Config{DatabaseURL: "sqlite::memory:"}, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AddModule(NewStaticModule()); err != nil {
		t.Fatal(err)
	}
	if err := s.Auth().EnsureAdmin("admin", "admin12345"); err != nil {
		t.Fatal(err)
	}
	users, err := db.ListUsers()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].Name != "admin" || !users[0].Enabled || users[0].Role != database.UserRoleAdmin {
		t.Fatalf("admin user: %+v", users)
	}

	login := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{"username":"admin","password":"admin12345"}`))
	login.Header.Set("Content-Type", "application/json")
	loginWriter := httptest.NewRecorder()
	s.Handler().ServeHTTP(loginWriter, login)
	if loginWriter.Code != http.StatusOK {
		t.Fatalf("login: status=%d body=%s", loginWriter.Code, loginWriter.Body.String())
	}
}
