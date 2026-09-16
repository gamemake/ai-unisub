package service

import (
	"ai-unisub/internal/common"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRouterPanicReturnsJSONError(t *testing.T) {
	var logs bytes.Buffer
	if err := common.InitLogging(common.LogConfig{Output: &logs}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = common.InitLogging(common.LogConfig{}) })
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
	decoder := json.NewDecoder(&logs)
	for _, expected := range []struct{ event, level string }{{"request_panic", "ERROR"}, {"http_access", "INFO"}} {
		var entry map[string]string
		if err := decoder.Decode(&entry); err != nil {
			t.Fatal(err)
		}
		if len(entry) != 5 || entry["module"] != "service" || entry["event"] != expected.event || entry["level"] != expected.level {
			t.Fatalf("unexpected log: %v", entry)
		}
	}
}
