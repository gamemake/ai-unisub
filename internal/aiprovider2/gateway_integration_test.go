package aiprovider2

import (
	"ai-unisub/internal/database"
	"ai-unisub/internal/proxy2"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProviderManagerGatewayEndToEnd(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.RequestURI() != "/v1/responses?test=1" {
			t.Errorf("upstream URL = %q", req.URL.RequestURI())
		}
		if got := req.Header.Get("Authorization"); got != "Bearer upstream-secret" {
			t.Errorf("upstream Authorization = %q", got)
		}
		var body struct {
			Model string `json:"model"`
		}
		if err := json.UnmarshalRead(req.Body, &body); err != nil {
			t.Error(err)
		}
		if body.Model != "gpt-5" {
			t.Errorf("upstream model = %q, want gpt-5", body.Model)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-RateLimit-Remaining-Requests", "9")
		_, _ = w.Write([]byte(`{"model":"gpt-5","usage":{"input_tokens":7,"output_tokens":3}}`))
	}))
	defer upstream.Close()

	db, err := database.NewDatabase("sqlite::memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Open(); err != nil {
		t.Fatal(err)
	}
	proxyManager := proxy2.NewManager(db)
	if err := proxyManager.Open(); err != nil {
		t.Fatal(err)
	}
	proxyGroupID, err := proxyManager.Create(proxy2.ProxyGroupConfig{Name: "gateway proxies", Proxies: []string{upstream.URL}})
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewProviderManager(db, proxyManager, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
		if err := proxyManager.Close(); err != nil {
			t.Error(err)
		}
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})

	if err := provider.SetOverlayConfig(t.Context(), "openai", []byte(`{"Mappings":[{"Pattern":"client-model","Target":"gpt-5"}]}`)); err != nil {
		t.Fatal(err)
	}
	config, err := json.Marshal(AccountConfig{
		Kind: AccountAPI, Name: "gateway test", Supplier: "openai", Enabled: true,
		APIEndpoint: "http://upstream.invalid/v1", APIKey: "upstream-secret", ProxyGroupID: proxyGroupID, MaxConcurrentConnections: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := provider.NewAccount(config)
	if err != nil {
		t.Fatal(err)
	}
	groupConfig, err := json.Marshal(AccountConfig{
		Kind: AccountGroup, Name: "gateway group", Enabled: true,
		Members: []GroupMember{{ID: account.ID, Weight: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	group, err := provider.NewAccount(groupConfig)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	user := &database.PersistedUser{Name: "gateway-user", Role: database.UserRoleUser, Enabled: true, PasswordHash: "unused", CreatedAt: now, UpdatedAt: now}
	if err := db.SaveUser(user); err != nil {
		t.Fatal(err)
	}
	key := &database.PersistedAPIKey{Name: "gateway-key", UserID: user.ID, AccountID: group.ID, Key: "client-secret", CreatedAt: now, UpdatedAt: now}
	if err := db.SaveAPIKey(key); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/responses?test=1", strings.NewReader(`{"model":"client-model","input":"hello"}`))
	req.Header.Set("Authorization", "Bearer client-secret")
	result := httptest.NewRecorder()
	provider.Handler().ServeHTTP(result, req)
	if result.Code != http.StatusOK {
		t.Fatalf("gateway status = %d, body = %s", result.Code, result.Body.String())
	}

	account.mu.RLock()
	quota := cloneQuota(account.Quota)
	account.mu.RUnlock()
	if len(quota.Items) != 1 || quota.Items[0].Name != "X-Ratelimit-Remaining-Requests" || quota.Items[0].Value != "9" {
		t.Fatalf("account quota = %+v", quota)
	}

	traces, total, err := db.QueryCallTraces(database.CallTraceFilter{TimeRange: database.TimeRange{Start: now.Add(-time.Minute), End: time.Now().UTC().Add(time.Minute)}}, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(traces) != 1 {
		t.Fatalf("trace count = %d/%d", len(traces), total)
	}
	trace := traces[0]
	if trace.UserID != user.ID || trace.AccountID != account.ID || trace.AIProviderType != "openai" || trace.HTTPErrorCode != http.StatusOK {
		t.Fatalf("trace identity/status = %+v", trace)
	}
	if trace.Model != "gpt-5" || trace.InputTokens != 7 || trace.OutputTokens != 3 {
		t.Fatalf("trace usage = %+v", trace)
	}
	proxyLogs, proxyTotal, err := db.QueryProxyLogs(database.ProxyLogFilter{
		GroupID:   proxyGroupID,
		TimeRange: database.TimeRange{Start: now.Add(-time.Minute), End: time.Now().UTC().Add(time.Minute)},
	}, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if proxyTotal != 1 || len(proxyLogs) != 1 || proxyLogs[0].AppType != "openai" || proxyLogs[0].HTTPErrorCode != http.StatusOK {
		t.Fatalf("proxy2 logs = %+v, total = %d", proxyLogs, proxyTotal)
	}
}
