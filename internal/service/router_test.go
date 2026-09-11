package service

import (
	"ai-unisub/internal/common"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRouterPanicReturnsJSONError(t *testing.T) {
	r := newRouter()
	r.add("/panic", RouteOptions{Auth: AuthNone, Name: "panic"}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("test panic")
	}))
	w := httptest.NewRecorder()

	r.handler(&Service{authSvc: &authService{}}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/panic", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if got := w.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != common.MessageInternalServerError {
		t.Fatalf("body = %v", body)
	}
}
