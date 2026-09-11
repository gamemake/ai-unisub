package service

import (
	"ai-unisub/internal/database"
	"ai-unisub/internal/oauth"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		if body["error"] != "oauth session not found" {
			t.Fatalf("error %v: body=%v", err, body)
		}
	}
}
