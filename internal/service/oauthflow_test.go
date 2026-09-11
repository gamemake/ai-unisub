package service

import (
	"ai-unisub/internal/common"
	"ai-unisub/internal/database"
	"ai-unisub/internal/oauth"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOAuthResultIsAuthenticatedAndOneTime(t *testing.T) {
	db := database.NewMemoryDatabase()
	user := &database.PersistedUser{ID: "user-1", Name: "one", Role: database.UserRoleUser}
	if err := db.SaveUser(user); err != nil {
		t.Fatal(err)
	}
	s, err := NewWithDependencies(Config{DatabaseURL: "sqlite::memory:"}, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AddModule(NewOAuthFlowModule()); err != nil {
		t.Fatal(err)
	}
	token, err := s.Auth().CreateSession(user)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.OAuthResults().Put(OAuthResult{SubjectID: user.ID, Service: oauth.OAuthServiceClaude, Credential: oauth.OAuthCredential{AccessToken: "access", RefreshToken: "refresh"}})
	if err != nil {
		t.Fatal(err)
	}
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/api/oauth/results/"+id, nil)
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	first := request()
	if first.Code != http.StatusOK || first.Body.String() == "" {
		t.Fatalf("first response: status=%d body=%s", first.Code, first.Body.String())
	}
	second := request()
	if second.Code != http.StatusNotFound {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
}

func TestOAuthStartUsesAndRejectsProxy(t *testing.T) {
	db := database.NewMemoryDatabase()
	user := &database.PersistedUser{ID: "user-1", Name: "one", Role: database.UserRoleAdmin, Enabled: true}
	if err := db.SaveUser(user); err != nil {
		t.Fatal(err)
	}
	s, err := NewWithDependencies(Config{DatabaseURL: "sqlite::memory:"}, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AddModule(NewOAuthFlowModule()); err != nil {
		t.Fatal(err)
	}
	token, err := s.Auth().CreateSession(user)
	if err != nil {
		t.Fatal(err)
	}
	request := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/oauth/dummy/start", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if got := request(`{"proxy":"ftp://127.0.0.1:21"}`); got.Code != http.StatusBadRequest {
		t.Fatalf("invalid proxy: status=%d body=%s", got.Code, got.Body.String())
	}
	if got := request(`{"proxy":"socks5://127.0.0.1:1080"}`); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"session_id"`) {
		t.Fatalf("valid proxy: status=%d body=%s", got.Code, got.Body.String())
	}
}

func TestOAuthAPIErrorUsesBadRequestForUnknownOrExpiredSession(t *testing.T) {
	for _, err := range []error{oauth.ErrSessionNotFound, oauth.ErrSessionExpired} {
		w := httptest.NewRecorder()
		oauthAPIError(w, err)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("error %v: status=%d, want %d", err, w.Code, http.StatusBadRequest)
		}
		var body map[string]string
		if decodeErr := json.Unmarshal(w.Body.Bytes(), &body); decodeErr != nil {
			t.Fatalf("error %v: invalid JSON response: %v", err, decodeErr)
		}
		if body["error"] != common.MessageOAuthSessionNotFound {
			t.Fatalf("error %v: body=%v", err, body)
		}
	}
}
