package unisub

import (
	"ai-unisub/internal/aiprovider"
	"ai-unisub/internal/database"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"reflect"
	"testing"
)

func TestAIProviderQuotaAPI(t *testing.T) {
	s := testApp(t)
	cookie := loginTestApp(t, s)
	ids := map[string]int{}
	for _, tc := range []struct{ name, adapter, config string }{
		{"demo", "dummy", `{}`},
		{"real", "codex", `{}`},
		{"group", "group", ``},
	} {
		config := tc.config
		if tc.adapter == "group" {
			config = `{"members":[{"id":` + pathID(ids["demo"]) + `,"weight":3},{"id":` + pathID(ids["real"]) + `,"weight":3}]}`
		}
		account := &database.PersistedAccount{Name: tc.name, AIProvider: tc.adapter, Config: []byte(config)}
		if err := s.Database().SaveAccount(account); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AIProviders().Create(account.ID, tc.adapter, []byte(config), nil); err != nil {
			t.Fatal(err)
		}
		ids[tc.name] = account.ID
	}
	readList := func() map[string]aiprovider.Quota {
		t.Helper()
		out := appRequest(s, "GET", "/api/ai-providers", "", cookie)
		var result struct {
			Items []struct {
				ID    int            `json:"id"`
				Name  string         `json:"name"`
				Quota jsontext.Value `json:"quota"`
			} `json:"items"`
		}
		if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &result) != nil || len(result.Items) != 3 {
			t.Fatalf("invalid list: %d %s", out.Code, out.Body.String())
		}
		cache := make(map[string]aiprovider.Quota)
		for _, item := range result.Items {
			if item.ID == ids["group"] {
				if len(item.Quota) != 0 {
					t.Fatalf("group must omit quota, got %s", item.Quota)
				}
				continue
			}
			var quota aiprovider.Quota
			if len(item.Quota) == 0 || json.Unmarshal(item.Quota, &quota) != nil {
				t.Fatalf("missing or invalid quota for %s", item.Name)
			}
			cache[item.Name] = quota
		}
		return cache
	}
	initial := readList()
	for id, cache := range initial {
		if cache.CacheStatus != aiprovider.QuotaCacheMissing || len(cache.Items) != 0 || !cache.UpdatedAt.IsZero() {
			t.Fatalf("list fabricated cache for %s: %+v", id, cache)
		}
	}

	for _, tc := range []struct {
		method, name string
		status       int
	}{
		{"GET", "demo", 405}, {"POST", "demo", 200},
		{"GET", "group", 405}, {"POST", "group", 501},
		{"POST", "real", 400}, {"POST", "unknown", 404}, {"DELETE", "demo", 405},
	} {
		before := readList()
		id := "unknown"
		if n, ok := ids[tc.name]; ok {
			id = pathID(n)
		}
		out := appRequest(s, tc.method, "/api/ai-providers/"+id+"/refresh-quota", "", cookie)
		if out.Code != tc.status {
			t.Fatalf("%+v: %d %s", tc, out.Code, out.Body.String())
		}
		if tc.name == "group" && !reflect.DeepEqual(before, readList()) {
			t.Fatal("group quota request changed a member cache")
		}
		if out.Code != 200 {
			continue
		}
		var result aiprovider.Quota
		if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if tc.name == "demo" && tc.method == "POST" && (result.CacheStatus != aiprovider.QuotaCacheFresh || len(result.Subscription) != 2 || len(result.Items) != 0) {
			t.Fatalf("missing demo data: %+v", result)
		}
		if tc.name == "demo" {
			cache := readList()
			if len(cache["demo"].Subscription) != 2 || !reflect.DeepEqual(cache["demo"].Subscription, result.Subscription) || cache["demo"].CacheStatus != aiprovider.QuotaCacheFresh {
				t.Fatalf("list did not retain refreshed data: %+v", cache)
			}
			accounts, err := s.Database().ListAccounts()
			if err != nil {
				t.Fatal(err)
			}
			var stored aiprovider.AIProviderState
			for _, account := range accounts {
				if account.ID == ids["demo"] {
					if json.Unmarshal(account.State, &stored) != nil {
						t.Fatalf("persisted state is not JSON: %s", account.State)
					}
					break
				}
			}
			if len(stored.Quota.Subscription) != 2 || !reflect.DeepEqual(stored.Quota.Subscription, result.Subscription) {
				t.Fatalf("refreshed quota was not persisted in state: %+v", stored)
			}
			assertAdminCallRecorded(t, s, ids["demo"], true)
		}
	}
}
