package unisub

import (
	"ai-unisub/internal/database"
	"encoding/json"
	"net/http"
	"testing"
)

func TestPersonalAPIPermissions(t *testing.T) {
	s := testApp(t)
	admin := loginTestApp(t, s)
	if out := appRequest(s, "POST", "/api/users", `{"name":"member","password":"member-password","role":"user"}`, admin); out.Code != 201 {
		t.Fatal(out.Body.String())
	}
	login := appRequest(s, "POST", "/api/login", `{"username":"member","password":"member-password"}`, nil)
	if login.Code != 200 {
		t.Fatal(login.Body.String())
	}
	member := login.Result().Cookies()[0]
	for _, path := range []string{"/api/users", "/api/users/admin/password", "/api/ai-providers", "/api/ai-providers/id", "/api/ai-providers/id/refresh-quota", "/api/providers", "/api/providers/id", "/api/ai-catalog", "/api/ai-catalog/kimi", "/api/proxy-groups", "/api/proxy-groups/errors", "/api/proxy-groups/test", "/api/usage/users", "/api/usage/subscriptions", "/api/oauth/dummy/start", "/api/oauth/dummy/status/id", "/api/oauth/dummy/poll/id", "/api/oauth/dummy/complete/id", "/api/oauth/results/id"} {
		for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
			t.Run(method+path, func(t *testing.T) {
				if out := appRequest(s, method, path, "{}", member); out.Code != http.StatusForbidden {
					t.Fatalf("status=%d body=%s", out.Code, out.Body.String())
				}
				if out := appRequest(s, method, path, "{}", nil); out.Code != http.StatusUnauthorized {
					t.Fatalf("anonymous status=%d", out.Code)
				}
			})
		}
	}
	if err := s.Database().SaveAccount(&database.PersistedAccount{ID: "option", Name: "Option", AIProvider: "api", Config: json.RawMessage(`{"api_key":"secret","api_endpoint":"https://private.invalid","enabled":true}`)}); err != nil {
		t.Fatal(err)
	}
	out := appRequest(s, "GET", "/api/keys/providers", "", member)
	var options struct {
		Items []map[string]any `json:"items"`
	}
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &options) != nil || len(options.Items) != 1 {
		t.Fatal(out.Code, out.Body.String())
	}
	item := options.Items[0]
	if len(item) != 4 || item["id"] != "option" || item["name"] != "Option" || item["provider"] != "api" || item["enabled"] != true {
		t.Fatal(item)
	}
	if out := appRequest(s, "PUT", "/api/keys/providers", "{}", member); out.Code != 405 {
		t.Fatal(out.Code)
	}
	for _, path := range []string{"/api/me", "/api/keys", "/api/calls?mine=0"} {
		if out := appRequest(s, "GET", path, "", member); out.Code != 200 {
			t.Fatal(path, out.Code)
		}
	}
	created := appRequest(s, "POST", "/api/keys", `{"name":"personal","account_id":"option","user_id":"admin"}`, member)
	var key struct {
		ID string `json:"id"`
	}
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &key) != nil {
		t.Fatal(created.Code, created.Body.String())
	}
	for _, method := range []string{"GET", "DELETE"} {
		if out := appRequest(s, method, "/api/keys/"+key.ID, "", admin); out.Code != 404 {
			t.Fatal("cross-user key access", method, out.Code)
		}
	}
	if out := appRequest(s, "GET", "/api/keys/"+key.ID, "", member); out.Code != 200 {
		t.Fatal(out.Code)
	}
	if out := appRequest(s, "DELETE", "/api/keys/"+key.ID, "", member); out.Code != 204 {
		t.Fatal(out.Code)
	}
}
