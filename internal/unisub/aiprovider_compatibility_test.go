package unisub

import (
	"ai-unisub/internal/database"
	"encoding/json"
	"testing"
)

func TestAIProviderLegacyRouteCompatibility(t *testing.T) {
	app := testApp(t)
	cookie := loginTestApp(t, app)
	created := appRequest(app, "POST", "/api/providers", `{"name":"legacy","provider":"dummy","config":{"enabled":true}}`, cookie)
	if created.Code != 201 {
		t.Fatalf("legacy create: %d %s", created.Code, created.Body.String())
	}
	var account database.PersistedAccount
	if err := json.Unmarshal(created.Body.Bytes(), &account); err != nil {
		t.Fatal(err)
	}
	if account.AIProvider != "dummy" {
		t.Fatalf("legacy JSON field not decoded: %+v", account)
	}
	for _, base := range []string{"/api/ai-providers", "/api/providers"} {
		response := appRequest(app, "GET", base, "", cookie)
		if response.Code != 200 {
			t.Fatalf("list %s: %d", base, response.Code)
		}
		var list struct {
			Items []database.PersistedAccount `json:"items"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		if len(list.Items) != 1 || list.Items[0].ID != account.ID || list.Items[0].AIProvider != "dummy" {
			t.Fatalf("list %s: %s", base, response.Body.String())
		}
	}
	updated := appRequest(app, "PUT", "/api/ai-providers/"+pathID(account.ID), `{"name":"renamed"}`, cookie)
	if updated.Code != 200 {
		t.Fatalf("canonical update: %d %s", updated.Code, updated.Body.String())
	}
	removed := appRequest(app, "DELETE", "/api/providers/"+pathID(account.ID), "", cookie)
	if removed.Code != 204 {
		t.Fatalf("legacy delete: %d %s", removed.Code, removed.Body.String())
	}
	if _, ok := app.AIProviders().GetAccount(account.ID); ok {
		t.Fatal("deleted AI provider remains in runtime")
	}
}
