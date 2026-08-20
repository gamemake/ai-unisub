package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminPageIsServedWithoutCaching(t *testing.T) {
	application, _ := testServer(t, "https://example.invalid/responses")
	request := httptest.NewRequest(http.MethodGet, "/admin", nil)
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
	for _, marker := range []string{"UniSub", "账号管理", "<h2>Usage</h2>", "accountUsageTable", "accountModalTitle", "page-keys", "apiKeyForm", "grokOAuthModal", "/admin/assets/admin.css", "/admin/assets/admin.js"} {
		if !strings.Contains(recorder.Body.String(), marker) {
			t.Errorf("admin page does not contain %q", marker)
		}
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
		{path: "/admin/assets/admin.css", contentType: "text/css", markers: []string{".login-shell", ".account-name"}},
		{path: "/admin/assets/admin.js", contentType: "application/javascript", markers: []string{"/admin/accounts", "/admin/api-keys", "sessionStorage", "renderAccountUsage", "renderAPIKeys", "renderQuota", "creditBalanceLine", "usagePercent", "refreshData", "usage/refresh", "<th>Usage</th>", "openEditAccount", "prettyJSON", "data-action=\"edit\"", "data-action=\"delete\"", "编辑账号", "ccswitch://v1/import", "openCCSwitch", "grokbuild", "/admin/providers/grok/oauth/device/start", "/admin/providers/grok/oauth/device/poll"}},
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
	if got := recorder.Header().Get("Location"); got != "/admin" {
		t.Fatalf("Location = %q", got)
	}
}
