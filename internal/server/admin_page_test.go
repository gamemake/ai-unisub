package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminPageIsServedWithoutCaching(t *testing.T) {
	application, _ := testServer(t, "https://example.invalid/responses")
	request := httptest.NewRequest(http.MethodGet, "/home", nil)
	recorder := httptest.NewRecorder()

	application.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := recorder.Header().Get("Content-Type"); !strings.Contains(got, "text/html") {
		t.Fatalf("Content-Type = %q", got)
	}
	for _, marker := range []string{"UniSub", "订阅管理", "accountTable", "accountModalTitle", "page-personal", "page-overview", "page-keys", "page-logs", "page-users", "page-security", "apiKeyForm", "userForm", "resetPasswordModal", "logAccountFilter", "按上游订阅筛选", "个人总览", `data-icon="home"`, "系统总览", "overviewNav", "logsNav", "personalStatKeys", "personalRecentCalls", "用户管理", "安全设置", "调用记录", "usageBySubscriptionTable", "usageByUserTable", "用户用量", "userUsageSubscriptionFilter", "所有订阅", "usage-query-icon", `aria-label="查询用户用量"`, "usageRange1D", "userUsageRange1D", "userUsageCustomFields", "grokOAuthModal", "pkceOAuthModal", "pkceOAuthCode", "logModal", "modal-shell fullscreen", "modal fullscreen", "/assets/admin.css", "/assets/admin.js", "logoutButton"} {
		if !strings.Contains(recorder.Body.String(), marker) {
			t.Errorf("admin page does not contain %q", marker)
		}
	}
	if strings.Contains(recorder.Body.String(), "subscriptionUsageTable") {
		t.Error("overview Usage table should have been moved to account management")
	}
	if strings.Contains(recorder.Body.String(), "<h2>Usage</h2>") {
		t.Error("overview Usage heading should have been removed")
	}
	if count := strings.Count(recorder.Body.String(), "＋ 添加订阅"); count != 1 {
		t.Errorf("admin page contains %d add-subscription buttons", count)
	}
	if strings.Contains(recorder.Body.String(), "topAddButton") {
		t.Error("removed top add-account button is still referenced")
	}
	if strings.Contains(recorder.Body.String(), "logProviderFilter") {
		t.Error("request log provider filter should be removed")
	}
	if !strings.Contains(recorder.Body.String(), "每把 Key 只绑定一个订阅") || !strings.Contains(recorder.Body.String(), "绑定订阅") {
		t.Error("API Key page should use subscription terminology")
	}
	if strings.Contains(recorder.Body.String(), "服务器集成") {
		t.Error("removed integration page is still referenced")
	}
}

func TestAdminAssetsAreEmbeddedAndServed(t *testing.T) {
	application, _ := testServer(t, "https://example.invalid/responses")
	tests := []struct {
		path        string
		contentType string
		markers     []string
	}{
		{path: "/assets/admin.css", contentType: "text/css", markers: []string{".login-shell", ".account-name", ".nav #overviewNav:before", ".log-pre", ".log-block-head", ".log-block.collapsed", ".modal.fullscreen", "grid-template-columns:78px minmax(0,1fr)", ".oauth-paste", ".actions .icon-btn", ".table-card>.card-head", "#userUsageSubscriptionFilter", "flex:0 0 200px", ".usage-query-icon svg", ".usage-sort", ".usage-sort-mark"}},
		{path: "/assets/admin.js", contentType: "application/javascript", markers: []string{"/api/subscriptions", "/api/api-keys", "/api/request-logs", "/api/usage/by-subscription", "/api/usage/by-user", "renderUsageByUser", "loadUsageByUser", "setUserUsageRange", "userUsageRange", "userUsageSubscriptionFilter", "fillUserUsageSubscriptionOptions", "userUsageQuery", "subscription_id", "sessionStorage", "overviewNav", "accountsNav", "usersNav", "logsNav", "renderPersonalOverview", "personalRecentCalls", "personalStatKeys", "landingResolved", "renderAccounts", "formatLocalTokens", "renderAPIKeys", "renderLogs", "logKeyLabel", "formatDuration", "logAccountFilter", "fillLogFilters", "renderQuota", "creditBalanceLine", "usagePercent", "refreshData", "usage/refresh", "loadUsageBySubscription", "usageSortHeader", "sortUsageRows", "usageSortClick", "data-usage-sort-key", "aria-sort", "<th>Usage</th>", "<th>Token</th>", "openEditAccount", "prettyJSON", "iconActionButton('edit'", "iconActionButton('delete'", "iconActionButton('refresh-usage'", "iconActionButton('ccswitch'", "canRefreshUsage", "刷新 Usage", "iconActionButton", "actionIcon", "'编辑'", "'删除'", "编辑账号", "ccswitch://v1/import", "openCCSwitch", "launchCCSwitch", "apiKey:plaintext", "grokbuild", "/api/users", "renderUsers", "reset-password", "/password", "openResetPasswordModal", "saveResetPassword", "/api/providers/grok/oauth/device/start", "/api/providers/grok/oauth/device/poll", "/api/providers/claude/oauth/start", "/api/providers/codex/oauth/exchange", "登录并绑定 Claude", "登录并绑定 Codex", "startPKCEOAuth", "submitPKCEOAuth", "pkceOAuthModal", "Request Headers", "Response Headers", "data-copy-index", "data-log-toggle", "formatLogHeaders", "缓存创建 ", " · 缓存读取 ", "输入 ", "合计 ", "总数 ", "<th>ID</th>", "<th>模型</th>", "日志 ID", "来源 IP", "client_ip", " 秒", " 分钟"}},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			recorder := httptest.NewRecorder()
			application.Handler().ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d", recorder.Code)
			}
			if got := recorder.Header().Get("Content-Type"); !strings.Contains(got, test.contentType) {
				t.Fatalf("Content-Type = %q", got)
			}
			if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q", got)
			}
			for _, marker := range test.markers {
				if !strings.Contains(recorder.Body.String(), marker) {
					t.Errorf("asset does not contain %q", marker)
				}
			}
			if test.path == "/assets/admin.js" && strings.Contains(recorder.Body.String(), "Grok Usage 已刷新") {
				t.Error("top refresh still bulk-refreshes Grok usage")
			}
			if test.path == "/assets/admin.js" && strings.Contains(recorder.Body.String(), `data-key-action="ccswitch"`) {
				t.Error("API key list still uses text action buttons")
			}
			if test.path == "/assets/admin.js" {
				body := recorder.Body.String()
				if strings.Contains(body, "logProviderFilter") {
					t.Error("request log provider filter logic should be removed")
				}
				if strings.Contains(body, `<option value="">全部账号</option>`) {
					t.Error("API Key or request log filter still uses all-accounts terminology")
				}
				if strings.Count(body, "<th>订阅</th>") < 2 || strings.Contains(body, "logMetaItem('账号'") {
					t.Error("API Key and request log views should label subscriptions consistently")
				}
				for _, legacyLabel := range []string{"缓存写 ", " · 缓存读 "} {
					if strings.Contains(body, legacyLabel) {
						t.Errorf("usage display still contains legacy label %q", legacyLabel)
					}
				}
				if !strings.Contains(body, "缓存创建 '+escapeHTML(formatNumber(log.cache_creation_tokens||0))+' · 缓存读取 '+escapeHTML(formatNumber(log.cache_read_tokens||0))") {
					t.Error("request log usage should show cache creation before cache read")
				}
				for _, usageTable := range []string{"subscription", "user"} {
					creationHeader := "usageSortHeader('" + usageTable + "','cache_creation_tokens','缓存创建'"
					readHeader := "usageSortHeader('" + usageTable + "','cache_read_tokens','缓存读取'"
					creationAt := strings.Index(body, creationHeader)
					readAt := strings.Index(body, readHeader)
					if creationAt < 0 || readAt < 0 || creationAt >= readAt {
						t.Errorf("%s usage should show sortable cache creation before cache read", usageTable)
					}
				}
			}
		})
	}
}

func TestRootRedirectsToAdmin(t *testing.T) {
	application, _ := testServer(t, "https://example.invalid/responses")
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	recorder := httptest.NewRecorder()

	application.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d", recorder.Code)
	}
	if got := recorder.Header().Get("Location"); got != "/home" {
		t.Fatalf("Location = %q", got)
	}
}
