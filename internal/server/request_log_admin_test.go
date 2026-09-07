package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
	"github.com/ai-unisub/ai-unisub/internal/repository"
)

func TestAdminRequestLogsCollection(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r1","model":"gpt-test","usage":{"input_tokens":6,"output_tokens":1}}`)
	}))
	defer upstream.Close()

	application, repo := testServer(t, upstream.URL+"/responses")
	_, key := createSubscriptionWithKey(t, repo, repository.CreateSubscriptionParams{
		Name: "log-account", Provider: model.ProviderCodex, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "upstream-token"},
	})

	proxy := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-test"}`))
	proxy.Header.Set("Authorization", "Bearer "+key)
	proxyRecorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(proxyRecorder, proxy)
	if proxyRecorder.Code != http.StatusOK {
		t.Fatalf("proxy status=%d body=%s", proxyRecorder.Code, proxyRecorder.Body.String())
	}

	adminToken, err := application.signer.issue("admin", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	list := httptest.NewRequest(http.MethodGet, "/api/request-logs", nil)
	list.Header.Set("Authorization", "Bearer "+adminToken)
	listRecorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(listRecorder, list)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	var listed struct {
		Total int `json:"total"`
		Data  []struct {
			ID     int64  `json:"id"`
			Day    string `json:"day"`
			Path   string `json:"path"`
			Status int    `json:"status_code"`
		} `json:"data"`
	}
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if listed.Total != 1 || len(listed.Data) != 1 || listed.Data[0].Path != "/v1/responses" || listed.Data[0].Status != 200 {
		t.Fatalf("list = %+v body=%s", listed, listRecorder.Body.String())
	}

	detail := httptest.NewRequest(http.MethodGet, "/api/request-logs/"+listed.Data[0].Day+"/"+strconv.FormatInt(listed.Data[0].ID, 10), nil)
	detail.Header.Set("Authorization", "Bearer "+adminToken)
	detailRecorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(detailRecorder, detail)
	if detailRecorder.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", detailRecorder.Code, detailRecorder.Body.String())
	}
	body := detailRecorder.Body.String()
	for _, marker := range []string{`gpt-test`, `input_tokens`, `request_body`, `response_body`, `request_headers`, `response_headers`, `r1`} {
		if !strings.Contains(body, marker) {
			t.Errorf("detail missing %s: %s", marker, body)
		}
	}
}

func TestMemberRequestLogsForbidden(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r1","model":"gpt-test","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer upstream.Close()

	application, repo := testServer(t, upstream.URL+"/responses")
	if _, err := repo.CreateUser(context.Background(), "member", "member-pass-ok", model.RoleUser); err != nil {
		t.Fatal(err)
	}
	_, key := createSubscriptionWithKey(t, repo, repository.CreateSubscriptionParams{
		Name: "member-account", Provider: model.ProviderCodex, AuthType: "oauth",
		Credentials: model.Credentials{AccessToken: "upstream-token"},
	})

	proxy := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-test"}`))
	proxy.Header.Set("Authorization", "Bearer "+key)
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, proxy)
	if recorder.Code != http.StatusOK {
		t.Fatalf("proxy status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	memberToken, err := application.signer.issue("member", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	list := httptest.NewRequest(http.MethodGet, "/api/request-logs", nil)
	list.Header.Set("Authorization", "Bearer "+memberToken)
	listRecorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(listRecorder, list)
	if listRecorder.Code != http.StatusForbidden {
		t.Fatalf("member list status=%d body=%s", listRecorder.Code, listRecorder.Body.String())
	}

	adminToken, err := application.signer.issue("admin", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	adminList := httptest.NewRequest(http.MethodGet, "/api/request-logs", nil)
	adminList.Header.Set("Authorization", "Bearer "+adminToken)
	adminListRecorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(adminListRecorder, adminList)
	if adminListRecorder.Code != http.StatusOK {
		t.Fatalf("admin list status=%d body=%s", adminListRecorder.Code, adminListRecorder.Body.String())
	}
	var adminListed struct {
		Total int `json:"total"`
		Data  []struct {
			ID  int64  `json:"id"`
			Day string `json:"day"`
		} `json:"data"`
	}
	if err := json.Unmarshal(adminListRecorder.Body.Bytes(), &adminListed); err != nil {
		t.Fatal(err)
	}
	if adminListed.Total != 1 || len(adminListed.Data) != 1 {
		t.Fatalf("admin list = %+v", adminListed)
	}
	denied := httptest.NewRequest(http.MethodGet, "/api/request-logs/"+adminListed.Data[0].Day+"/"+strconv.FormatInt(adminListed.Data[0].ID, 10), nil)
	denied.Header.Set("Authorization", "Bearer "+memberToken)
	deniedRecorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(deniedRecorder, denied)
	if deniedRecorder.Code != http.StatusForbidden {
		t.Fatalf("member detail status=%d body=%s", deniedRecorder.Code, deniedRecorder.Body.String())
	}
}
