package unisub

import (
	"net/http"
	"strings"
	"testing"
)

func TestPublicAuthJSONContract(t *testing.T) {
	s := testApp(t)
	for _, path := range []string{"/api/login", "/api/logout"} {
		for _, method := range []string{"GET", "HEAD", "PUT"} {
			out := appRequest(s, method, path, "", nil)
			if out.Code != 405 || out.Header().Get("Allow") != "POST" || out.Header().Get("Location") != "" || out.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("method %s %s: %d", method, path, out.Code)
			}
		}
	}
	for _, body := range []string{`{}`, `null`, `{"username":"admin","password":"x"} {}`, strings.Repeat("x", (1<<20)+1)} {
		if out := appRequest(s, "POST", "/api/login", body, nil); out.Code != 400 {
			t.Fatalf("invalid login: %d", out.Code)
		}
	}
	for _, body := range []string{`{"username":"missing","password":"wrong"}`, `{"username":"admin","password":"wrong"}`} {
		out := appRequest(s, "POST", "/api/login", body, nil)
		if out.Code != 401 || out.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("bad credentials not rejected")
		}
	}
	cookie := loginTestApp(t, s)
	for _, c := range []*http.Cookie{cookie, cookie, nil, {Name: "session", Value: "expired"}} {
		out := appRequest(s, "POST", "/api/logout", "", c)
		if out.Code != 200 || out.Header().Get("Location") != "" || !strings.Contains(out.Body.String(), `"status":"ok"`) {
			t.Fatalf("logout not idempotent: %d", out.Code)
		}
		cookies := out.Result().Cookies()
		if len(cookies) != 1 || cookies[0].MaxAge != -1 {
			t.Fatal("logout did not expire cookie")
		}
	}
	if out := appRequest(s, "GET", "/api/me", "", cookie); out.Code != 401 {
		t.Fatal("logout did not invalidate server session")
	}
	if out := appRequest(s, "GET", "/api/users", "", nil); out.Code != 401 {
		t.Fatal("management API became public")
	}
}
