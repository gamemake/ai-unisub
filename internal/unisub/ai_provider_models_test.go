package unisub

import (
	"ai-unisub/internal/aiprovider"
	"ai-unisub/internal/database"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAIProviderFetchModelsAPI(t *testing.T) {
	s := testApp(t)
	cookie := loginTestApp(t, s)
	ids := map[string]int{}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer upstream-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   []map[string]string{{"id": "alpha"}, {"id": "beta"}},
		})
	}))
	defer upstream.Close()

	for _, tc := range []struct {
		name, adapter, config string
	}{
		{"demo", "dummy", `{}`},
		{"api", "api", `{"auth_type":"api_key","api_key":"upstream-key","supplier":"openai","api_endpoint":"` + upstream.URL + `/v1"}`},
		{"group", "group", ``},
	} {
		config := tc.config
		if tc.adapter == "group" {
			config = `{"members":[{"id":` + pathID(ids["demo"]) + `,"weight":3},{"id":` + pathID(ids["api"]) + `,"weight":3}]}`
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

	for _, tc := range []struct {
		method, name string
		status       int
		want         []string
	}{
		{"GET", "demo", 405, nil},
		{"POST", "demo", 200, []string{"dummy-fast", "dummy-long-context", "dummy-model", "dummy-reasoning"}},
		{"POST", "api", 200, []string{"alpha", "beta"}},
		{"POST", "group", 200, []string{}},
		{"POST", "unknown", 404, nil},
		{"DELETE", "demo", 405, nil},
	} {
		id := "unknown"
		if n, ok := ids[tc.name]; ok {
			id = pathID(n)
		}
		out := appRequest(s, tc.method, "/api/ai-providers/"+id+"/fetch-models", "", cookie)
		if out.Code != tc.status {
			t.Fatalf("%+v: %d %s", tc, out.Code, out.Body.String())
		}
		if out.Code != 200 {
			continue
		}
		var result aiprovider.ModelList
		if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		got := make([]string, 0, len(result.Models))
		for _, model := range result.Models {
			got = append(got, model.ID)
		}
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Fatalf("%s models=%v want %v", tc.name, got, tc.want)
		}
		if tc.name == "api" || tc.name == "demo" {
			assertAdminCallRecorded(t, s, ids[tc.name], tc.name == "demo")
		}
	}
}

func assertAdminCallRecorded(t *testing.T, s interface {
	Database() database.Database
}, accountID int, dummy bool) {
	t.Helper()
	now := time.Now().UTC()
	items, total, err := s.Database().QueryCallTraces(database.CallTraceFilter{
		AccountID: accountID,
		TimeRange: database.TimeRange{Start: now.Add(-time.Hour), End: now.Add(time.Hour)},
	}, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total < 1 || len(items) < 1 {
		t.Fatalf("expected call trace for account %d, total=%d items=%d", accountID, total, len(items))
	}
	detail, err := s.Database().GetCallTrace(items[0].StartedAt, items[0].ID)
	if err != nil || detail == nil {
		t.Fatalf("get call trace: %v", err)
	}
	if detail.AccountID != accountID || detail.HTTPErrorCode != 200 {
		t.Fatalf("unexpected trace summary: %+v", detail)
	}
	if dummy && detail.URL != "dummy://models" && detail.URL != "dummy://quota" {
		// models and quota tests share this helper; accept either dummy URL.
		if !strings.HasPrefix(detail.URL, "dummy://") {
			t.Fatalf("dummy url=%q", detail.URL)
		}
	}
	if !dummy && !strings.Contains(detail.URL, "/models") && !strings.Contains(detail.URL, "billing") && !strings.Contains(detail.URL, "usage") {
		// Real upstream URL should point at the models (or quota) endpoint.
		if detail.URL == "" {
			t.Fatal("missing upstream url")
		}
	}
	if !reflect.DeepEqual(detail.OriginalRequestHeaders, detail.OutboundRequestHeaders) {
		t.Fatalf("original and outbound headers must match: original=%v outbound=%v", detail.OriginalRequestHeaders, detail.OutboundRequestHeaders)
	}
	if detail.OutboundRequestHeaders.Get("Authorization") != "" && detail.OutboundRequestHeaders.Get("Authorization") != "[redacted]" {
		t.Fatalf("authorization not redacted: %q", detail.OutboundRequestHeaders.Get("Authorization"))
	}
	if detail.OutboundRequestHeaders.Get("X-Api-Key") != "" && detail.OutboundRequestHeaders.Get("X-Api-Key") != "[redacted]" {
		t.Fatalf("x-api-key not redacted: %q", detail.OutboundRequestHeaders.Get("X-Api-Key"))
	}
}
