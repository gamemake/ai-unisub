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
	for _, tc := range []struct{ id, adapter, config string }{
		{"demo", "dummy", `{}`},
		{"real", "codex", `{}`},
		{"group", "group", `{"members":[{"id":"demo","weight":3},{"id":"real","weight":3}]}`},
	} {
		if _, err := s.AIProviders().Create(tc.id, tc.adapter, []byte(tc.config)); err != nil {
			t.Fatal(err)
		}
		if err := s.Database().SaveAccount(&database.PersistedAccount{ID: tc.id, Name: tc.id, AIProvider: tc.adapter, Config: []byte(tc.config)}); err != nil {
			t.Fatal(err)
		}
	}
	readList := func() map[string]aiprovider.Quota {
		t.Helper()
		out := appRequest(s, "GET", "/api/ai-providers", "", cookie)
		var result struct {
			Items []struct {
				ID    string         `json:"id"`
				Quota jsontext.Value `json:"quota"`
			} `json:"items"`
		}
		if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &result) != nil || len(result.Items) != 3 {
			t.Fatalf("invalid list: %d %s", out.Code, out.Body.String())
		}
		cache := make(map[string]aiprovider.Quota)
		for _, item := range result.Items {
			if item.ID == "group" {
				if len(item.Quota) != 0 {
					t.Fatalf("group must omit quota, got %s", item.Quota)
				}
				continue
			}
			var quota aiprovider.Quota
			if len(item.Quota) == 0 || json.Unmarshal(item.Quota, &quota) != nil {
				t.Fatalf("missing or invalid quota for %s", item.ID)
			}
			cache[item.ID] = quota
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
		method, id string
		status     int
	}{
		{"GET", "demo", 405}, {"POST", "demo", 200},
		{"GET", "group", 405}, {"POST", "group", 501},
		{"POST", "real", 400}, {"POST", "unknown", 404}, {"DELETE", "demo", 405},
	} {
		before := readList()
		out := appRequest(s, tc.method, "/api/ai-providers/"+tc.id+"/refresh-quota", "", cookie)
		if out.Code != tc.status {
			t.Fatalf("%+v: %d %s", tc, out.Code, out.Body.String())
		}
		if tc.id == "group" && !reflect.DeepEqual(before, readList()) {
			t.Fatal("group quota request changed a member cache")
		}
		if out.Code != 200 {
			continue
		}
		var result aiprovider.Quota
		if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if tc.id == "demo" && tc.method == "POST" && (result.CacheStatus != aiprovider.QuotaCacheFresh || len(result.Subscription) != 2 || len(result.Items) != 0) {
			t.Fatalf("missing demo data: %+v", result)
		}
		if tc.id == "demo" {
			cache := readList()
			if len(cache["demo"].Subscription) != 2 || !reflect.DeepEqual(cache["demo"].Subscription, result.Subscription) || cache["demo"].CacheStatus != aiprovider.QuotaCacheFresh {
				t.Fatalf("list did not retain refreshed data: %+v", cache)
			}
		}
	}
}
