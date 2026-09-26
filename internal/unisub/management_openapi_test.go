package unisub

import (
	"ai-unisub/internal/database"
	framework "ai-unisub/internal/service"
	"bytes"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestManagementOpenAPIContainsDashboardOperations(t *testing.T) {
	document := ManagementOpenAPI()
	if document.OpenAPI != "3.1.0" {
		t.Fatalf("OpenAPI version = %q", document.OpenAPI)
	}
	for _, path := range []string{
		"/api/login", "/api/users", "/api/accounts/{id}", "/api/suppliers/{id}",
		"/api/keys/{id}/config/{client}", "/api/calls/{day}/{id}",
		"/api/oauth/{service}/start",
	} {
		if document.Paths[path] == nil {
			t.Errorf("OpenAPI path %q is missing", path)
		}
	}
	if document.Components.SecuritySchemes["sessionCookie"] == nil {
		t.Error("sessionCookie security scheme is missing")
	}
	if _, err := json.Marshal(document); err != nil {
		t.Fatalf("marshal OpenAPI: %v", err)
	}
}

func TestManagementDocumentationRequiresAdminSession(t *testing.T) {
	db, err := database.NewDatabase("sqlite::memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Open(); err != nil {
		t.Fatal(err)
	}
	svc, err := framework.NewWithDependencies(framework.Config{}, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := svc.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()
	if err := svc.AddModule(NewAPIModule()); err != nil {
		t.Fatal(err)
	}
	if err := svc.Auth().EnsureAdmin("admin", "admin-password"); err != nil {
		t.Fatal(err)
	}
	member := &database.PersistedUser{
		Name:         "member",
		Role:         database.UserRoleUser,
		Enabled:      true,
		PasswordHash: framework.HashPassword("member-password"),
	}
	if err := db.SaveUser(member); err != nil {
		t.Fatal(err)
	}

	call := func(method, path string, body []byte, cookies []*http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		svc.Handler().ServeHTTP(response, request)
		return response
	}

	if response := call(http.MethodGet, managementOpenAPIPath+".json", nil, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous OpenAPI status = %d", response.Code)
	}
	memberLogin := call(http.MethodPost, "/api/login", []byte(`{"username":"member","password":"member-password"}`), nil)
	if memberLogin.Code != http.StatusOK {
		t.Fatalf("member login status = %d, body = %s", memberLogin.Code, memberLogin.Body.String())
	}
	if response := call(http.MethodGet, managementDocsPath, nil, memberLogin.Result().Cookies()); response.Code != http.StatusForbidden {
		t.Fatalf("member docs status = %d", response.Code)
	}
	login := call(http.MethodPost, "/api/login", []byte(`{"username":"admin","password":"admin-password"}`), nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", login.Code, login.Body.String())
	}
	cookies := login.Result().Cookies()
	if response := call(http.MethodGet, managementOpenAPIPath+".json", nil, cookies); response.Code != http.StatusOK {
		t.Fatalf("OpenAPI status = %d, body = %s", response.Code, response.Body.String())
	}
	if response := call(http.MethodGet, managementDocsPath, nil, cookies); response.Code != http.StatusOK {
		t.Fatalf("docs status = %d, body = %s", response.Code, response.Body.String())
	}
}
