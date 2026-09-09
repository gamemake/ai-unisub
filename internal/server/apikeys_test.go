package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/model"
	"github.com/ai-unisub/ai-unisub/internal/repository"
)

func TestAPIKeyCRUDAllowsMultipleKeysPerSubscription(t *testing.T) {
	application, repo := testServer(t, "https://example.invalid/responses")
	account, err := repo.CreateSubscription(context.Background(), repository.CreateSubscriptionParams{
		Name: "multi-key", Provider: model.ProviderGrok,
		Credentials: model.Credentials{AccessToken: "token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	adminToken, err := application.signer.issue("admin", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	users, err := repo.ListUsers(context.Background())
	if err != nil || len(users) != 1 {
		t.Fatalf("users=%+v err=%v", users, err)
	}

	first := postJSON(t, application, adminToken, "/api/api-keys", `{"subscription_id":`+strconv.FormatInt(account.ID, 10)+`,"name":"team-a","rpm_limit":20}`)
	if first.Code != http.StatusCreated {
		t.Fatalf("create first status=%d body=%s", first.Code, first.Body.String())
	}
	second := postJSON(t, application, adminToken, "/api/api-keys", `{"subscription_id":`+strconv.FormatInt(account.ID, 10)+`,"name":"team-b"}`)
	if second.Code != http.StatusCreated {
		t.Fatalf("create second status=%d body=%s", second.Code, second.Body.String())
	}

	var created struct {
		APIKey string `json:"api_key"`
		Key    struct {
			ID             int64  `json:"id"`
			SubscriptionID int64  `json:"subscription_id"`
			UserID         *int64 `json:"user_id"`
		} `json:"key"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Key.SubscriptionID != account.ID || created.Key.UserID == nil || *created.Key.UserID != users[0].ID || created.APIKey == "" {
		t.Fatalf("created key = %+v", created)
	}

	list := httptest.NewRequest(http.MethodGet, "/api/api-keys", nil)
	list.Header.Set("Authorization", "Bearer "+adminToken)
	listRecorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(listRecorder, list)
	if listRecorder.Code != http.StatusOK || !bytes.Contains(listRecorder.Body.Bytes(), []byte(`"team-a"`)) || !bytes.Contains(listRecorder.Body.Bytes(), []byte(`"team-b"`)) {
		t.Fatalf("list status=%d body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	if bytes.Contains(listRecorder.Body.Bytes(), []byte(created.APIKey)) {
		t.Fatalf("list leaked plaintext api key: %s", listRecorder.Body.String())
	}

	detail := httptest.NewRequest(http.MethodGet, "/api/api-keys/"+strconv.FormatInt(created.Key.ID, 10), nil)
	detail.Header.Set("Authorization", "Bearer "+adminToken)
	detailRecorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(detailRecorder, detail)
	if detailRecorder.Code != http.StatusOK || !bytes.Contains(detailRecorder.Body.Bytes(), []byte(created.APIKey)) {
		t.Fatalf("detail status=%d body=%s", detailRecorder.Code, detailRecorder.Body.String())
	}

	reset := postJSON(t, application, adminToken, "/api/api-keys/"+strconv.FormatInt(created.Key.ID, 10)+"/reset", `{}`)
	if reset.Code != http.StatusOK {
		t.Fatalf("reset status=%d body=%s", reset.Code, reset.Body.String())
	}
	var resetBody struct {
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal(reset.Body.Bytes(), &resetBody); err != nil || resetBody.APIKey == "" || resetBody.APIKey == created.APIKey {
		t.Fatalf("reset body = %s", reset.Body.String())
	}

	del := httptest.NewRequest(http.MethodDelete, "/api/api-keys/"+strconv.FormatInt(created.Key.ID, 10), nil)
	del.Header.Set("Authorization", "Bearer "+adminToken)
	delRecorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(delRecorder, del)
	if delRecorder.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", delRecorder.Code, delRecorder.Body.String())
	}
}

func postJSON(t *testing.T, application *Server, token, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	return recorder
}
