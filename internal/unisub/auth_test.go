package unisub

import (
	"ai-unisub/internal/database"
	framework "ai-unisub/internal/service"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnsureAdminCreatesEnabledUserThatCanLogin(t *testing.T) {
	db := testDatabase(t)
	s, err := framework.NewWithDependencies(framework.Config{DatabaseURL: "sqlite::memory:"}, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AddModule(NewAPIModule()); err != nil {
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

	login := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"admin","password":"admin12345"}`))
	login.Header.Set("Content-Type", "application/json")
	loginWriter := httptest.NewRecorder()
	s.Handler().ServeHTTP(loginWriter, login)
	if loginWriter.Code != http.StatusOK {
		t.Fatalf("login: status=%d body=%s", loginWriter.Code, loginWriter.Body.String())
	}
}
