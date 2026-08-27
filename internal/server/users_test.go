package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
)

func TestAdminCanCreateAndListUsers(t *testing.T) {
	application, _ := testServer(t, "https://example.invalid/responses")
	adminToken, err := application.signer.issue("admin", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	created := postJSON(t, application, adminToken, "/api/users", `{"username":"operator","password":"operator-pass-ok","role":"user"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	list := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	list.Header.Set("Authorization", "Bearer "+adminToken)
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, list)
	if recorder.Code != http.StatusOK || !bytes.Contains(recorder.Body.Bytes(), []byte(`"operator"`)) {
		t.Fatalf("list status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMemberCannotManageUsers(t *testing.T) {
	application, repo := testServer(t, "https://example.invalid/responses")
	if _, err := repo.CreateUser(context.Background(), "member", "member-pass-ok", model.RoleUser); err != nil {
		t.Fatal(err)
	}
	memberToken, err := application.signer.issue("member", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	list := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	list.Header.Set("Authorization", "Bearer "+memberToken)
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, list)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestCannotDeleteLastAdmin(t *testing.T) {
	application, repo := testServer(t, "https://example.invalid/responses")
	users, err := repo.ListUsers(context.Background())
	if err != nil || len(users) != 1 {
		t.Fatalf("users=%+v err=%v", users, err)
	}
	adminToken, err := application.signer.issue("admin", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodDelete, "/api/users/"+strconv.FormatInt(users[0].ID, 10), nil)
	request.Header.Set("Authorization", "Bearer "+adminToken)
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest && recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestUserLoginAndMe(t *testing.T) {
	application, _ := testServer(t, "https://example.invalid/responses")
	login := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewBufferString(`{"username":"admin","password":"test-password-ok"}`))
	login.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, login)
	if recorder.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Token string     `json:"token"`
		User  model.User `json:"user"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Token == "" || body.User.Username != "admin" || body.User.Role != model.RoleAdmin {
		t.Fatalf("login body = %+v", body)
	}
	me := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	me.Header.Set("Authorization", "Bearer "+body.Token)
	meRecorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(meRecorder, me)
	if meRecorder.Code != http.StatusOK || !bytes.Contains(meRecorder.Body.Bytes(), []byte(`"admin"`)) {
		t.Fatalf("me status=%d body=%s", meRecorder.Code, meRecorder.Body.String())
	}
}
