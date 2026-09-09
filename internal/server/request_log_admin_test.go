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
		Name: "log-account", Provider: model.ProviderCodex,
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

func TestMemberRequestLogsScopedToSelf(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r1","model":"gpt-test","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer upstream.Close()

	application, repo := testServer(t, upstream.URL+"/responses")
	ctx := context.Background()
	member, err := repo.CreateUser(ctx, "member", "member-pass-ok", model.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	other, err := repo.CreateUser(ctx, "other", "other-pass-ok", model.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	subscription, err := repo.CreateSubscription(ctx, repository.CreateSubscriptionParams{
		Name: "shared", Provider: model.ProviderCodex,
		Credentials: model.Credentials{AccessToken: "upstream-token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	memberKey, memberPlain, err := repo.CreateAPIKey(ctx, repository.CreateAPIKeyParams{
		SubscriptionID: subscription.ID, UserID: &member.ID, Name: "member-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	otherKey, otherPlain, err := repo.CreateAPIKey(ctx, repository.CreateAPIKeyParams{
		SubscriptionID: subscription.ID, UserID: &other.ID, Name: "other-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = memberKey

	for _, key := range []string{memberPlain, otherPlain} {
		proxy := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-test"}`))
		proxy.Header.Set("Authorization", "Bearer "+key)
		recorder := httptest.NewRecorder()
		application.Handler().ServeHTTP(recorder, proxy)
		if recorder.Code != http.StatusOK {
			t.Fatalf("proxy status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}

	memberToken, err := application.signer.issue("member", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	list := httptest.NewRequest(http.MethodGet, "/api/request-logs?q=other", nil)
	list.Header.Set("Authorization", "Bearer "+memberToken)
	listRecorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(listRecorder, list)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("member list status=%d body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	var memberListed struct {
		Total int `json:"total"`
		Data  []struct {
			ID       int64  `json:"id"`
			Day      string `json:"day"`
			Username string `json:"username"`
			UserID   *int64 `json:"user_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &memberListed); err != nil {
		t.Fatal(err)
	}
	// Username search cannot reveal other users; member is still scoped to self.
	if memberListed.Total != 0 {
		t.Fatalf("member username search for other leaked rows: %+v", memberListed)
	}

	ownList := httptest.NewRequest(http.MethodGet, "/api/request-logs", nil)
	ownList.Header.Set("Authorization", "Bearer "+memberToken)
	ownListRecorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(ownListRecorder, ownList)
	if ownListRecorder.Code != http.StatusOK {
		t.Fatalf("member own list status=%d body=%s", ownListRecorder.Code, ownListRecorder.Body.String())
	}
	if err := json.Unmarshal(ownListRecorder.Body.Bytes(), &memberListed); err != nil {
		t.Fatal(err)
	}
	if memberListed.Total != 1 || len(memberListed.Data) != 1 || memberListed.Data[0].Username != "member" {
		t.Fatalf("member own list = %+v", memberListed)
	}
	if memberListed.Data[0].UserID == nil || *memberListed.Data[0].UserID != member.ID {
		t.Fatalf("member own user_id = %+v", memberListed.Data[0])
	}

	deniedFilter := httptest.NewRequest(http.MethodGet, "/api/request-logs?subscription_id="+strconv.FormatInt(subscription.ID, 10), nil)
	deniedFilter.Header.Set("Authorization", "Bearer "+memberToken)
	deniedFilterRecorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(deniedFilterRecorder, deniedFilter)
	if deniedFilterRecorder.Code != http.StatusForbidden {
		t.Fatalf("member subscription filter status=%d body=%s", deniedFilterRecorder.Code, deniedFilterRecorder.Body.String())
	}

	adminToken, err := application.signer.issue("admin", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	adminList := httptest.NewRequest(http.MethodGet, "/api/request-logs?q=other", nil)
	adminList.Header.Set("Authorization", "Bearer "+adminToken)
	adminListRecorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(adminListRecorder, adminList)
	if adminListRecorder.Code != http.StatusOK {
		t.Fatalf("admin list status=%d body=%s", adminListRecorder.Code, adminListRecorder.Body.String())
	}
	var adminListed struct {
		Total int `json:"total"`
		Data  []struct {
			ID       int64  `json:"id"`
			Day      string `json:"day"`
			Username string `json:"username"`
		} `json:"data"`
	}
	if err := json.Unmarshal(adminListRecorder.Body.Bytes(), &adminListed); err != nil {
		t.Fatal(err)
	}
	if adminListed.Total != 1 || len(adminListed.Data) != 1 || adminListed.Data[0].Username != "other" {
		t.Fatalf("admin username search = %+v", adminListed)
	}

	ownDetail := httptest.NewRequest(http.MethodGet, "/api/request-logs/"+memberListed.Data[0].Day+"/"+strconv.FormatInt(memberListed.Data[0].ID, 10), nil)
	ownDetail.Header.Set("Authorization", "Bearer "+memberToken)
	ownDetailRecorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(ownDetailRecorder, ownDetail)
	if ownDetailRecorder.Code != http.StatusOK {
		t.Fatalf("member own detail status=%d body=%s", ownDetailRecorder.Code, ownDetailRecorder.Body.String())
	}

	denied := httptest.NewRequest(http.MethodGet, "/api/request-logs/"+adminListed.Data[0].Day+"/"+strconv.FormatInt(adminListed.Data[0].ID, 10), nil)
	denied.Header.Set("Authorization", "Bearer "+memberToken)
	deniedRecorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(deniedRecorder, denied)
	if deniedRecorder.Code != http.StatusNotFound {
		t.Fatalf("member other detail status=%d body=%s", deniedRecorder.Code, deniedRecorder.Body.String())
	}

	_ = otherKey
}
