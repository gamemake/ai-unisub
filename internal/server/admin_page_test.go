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
	for _, marker := range []string{"UniSub", "账号管理", "accountTable", "accountModalTitle", "page-keys", "page-logs", "page-users", "page-security", "apiKeyForm", "userForm", "resetPasswordModal", "用户管理", "安全设置", "调用记录", "grokOAuthModal", "logModal", "modal-shell fullscreen", "modal fullscreen", "/assets/admin.css", "/assets/admin.js"} {
		if !strings.Contains(recorder.Body.String(), marker) {
			t.Errorf("admin page does not contain %q", marker)
		}
	}
	if strings.Contains(recorder.Body.String(), "accountUsageTable") {
		t.Error("overview Usage table should have been moved to account management")
	}
	if strings.Contains(recorder.Body.String(), "<h2>Usage</h2>") {
		t.Error("overview Usage heading should have been removed")
	}
	if count := strings.Count(recorder.Body.String(), "＋ 添加账号"); count != 1 {
		t.Errorf("admin page contains %d add-account buttons", count)
	}
	if strings.Contains(recorder.Body.String(), "topAddButton") {
		t.Error("removed top add-account button is still referenced")
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
		{path: "/assets/admin.css", contentType: "text/css", markers: []string{".login-shell", ".account-name", ".nav #accountsNav:before", ".log-pre", ".log-block-head", ".log-block.collapsed", ".modal.fullscreen"}},
		{path: "/assets/admin.js", contentType: "application/javascript", markers: []string{"/api/accounts", "/api/api-keys", "/api/request-logs", "sessionStorage", "accountsNav", "usersNav", "renderAccounts", "formatLocalTokens", "renderAPIKeys", "renderLogs", "formatDuration", "created_by_user_id===currentUser.id", "renderQuota", "creditBalanceLine", "usagePercent", "refreshData", "usage/refresh", "<th>Usage</th>", "<th>Token</th>", "openEditAccount", "prettyJSON", "data-action=\"edit\"", "data-action=\"delete\"", "编辑账号", "ccswitch://v1/import", "openCCSwitch", "launchCCSwitch", "apiKey:plaintext", "grokbuild", "/api/users", "renderUsers", "reset-password", "/password", "openResetPasswordModal", "saveResetPassword", "/api/providers/grok/oauth/device/start", "/api/providers/grok/oauth/device/poll", "Request Headers", "Response Headers", "data-copy-index", "data-log-toggle", "formatLogHeaders", "缓存读", "缓存写", "缓存创建", "缓存读取", "输入 ", "合计 ", "总数 ", "<th>ID</th>", "<th>模型</th>", "日志 ID", "来源 IP", "client_ip", " 秒", " 分钟"}},
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
