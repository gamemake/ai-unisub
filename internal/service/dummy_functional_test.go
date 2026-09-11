package service

import (
	"ai-unisub/internal/database"
	"ai-unisub/internal/provider"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type dummyProvider struct {
	config provider.ProviderConfig
}

func (p *dummyProvider) Config() provider.ProviderConfig { return p.config }
func (p *dummyProvider) UpdateConfig(raw json.RawMessage) error {
	var config provider.ProviderConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return err
	}
	config.ID, config.Enabled = p.config.ID, true
	p.config = config
	return nil
}
func (p *dummyProvider) Handle(r *http.Request, recorder provider.APICallRecorder) {
	body, _ := io.ReadAll(r.Body)
	if recorder != nil {
		recorder(&provider.ProviderCallTrace{URL: r.URL.String(), RequestBody: body, Model: "dummy-model"})
	}
}
func (p *dummyProvider) FetchUsage(context.Context) ([]provider.UsageItem, error) {
	return []provider.UsageItem{{Name: "requests", Value: "1"}}, nil
}
func (p *dummyProvider) ResetUsage(context.Context) error { return nil }

func TestDummyUserProviderAndAPIKeyFlow(t *testing.T) {
	db := database.NewMemoryDatabase()
	admin := &database.PersistedUser{ID: "admin", Name: "admin", Role: database.UserRoleAdmin, Enabled: true, PasswordHash: hashPassword("password-123")}
	if err := db.SaveUser(admin); err != nil {
		t.Fatal(err)
	}
	providers := provider.NewProviderManager()
	var created *dummyProvider
	if err := providers.Register("dummy", func(id string, raw json.RawMessage) (provider.Provider, error) {
		var config provider.ProviderConfig
		if err := json.Unmarshal(raw, &config); err != nil {
			return nil, err
		}
		created = &dummyProvider{config: provider.ProviderConfig{ID: id, Name: config.Name, Enabled: true}}
		return created, nil
	}); err != nil {
		t.Fatal(err)
	}
	s, err := NewWithDependencies(Config{DatabaseURL: "sqlite::memory:"}, db, providers)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AddModule(NewAPIModule()); err != nil {
		t.Fatal(err)
	}
	if err := s.AddModule(NewOAuthFlowModule()); err != nil {
		t.Fatal(err)
	}
	token, err := s.Auth().CreateSession(admin)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	oauthStart := request(http.MethodPost, "/api/oauth/dummy/start", `{}`)
	if oauthStart.Code != http.StatusOK || !strings.Contains(oauthStart.Body.String(), `"session_id"`) {
		t.Fatalf("start dummy oauth: status=%d body=%s", oauthStart.Code, oauthStart.Body.String())
	}
	var oauthSession struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(oauthStart.Body.Bytes(), &oauthSession); err != nil {
		t.Fatal(err)
	}
	oauthPoll := request(http.MethodPost, "/api/oauth/dummy/poll/"+oauthSession.SessionID, `{}`)
	if oauthPoll.Code != http.StatusAccepted {
		t.Fatalf("first dummy oauth poll: status=%d body=%s", oauthPoll.Code, oauthPoll.Body.String())
	}
	oauthPoll = request(http.MethodPost, "/api/oauth/dummy/poll/"+oauthSession.SessionID, `{}`)
	if oauthPoll.Code != http.StatusOK || !strings.Contains(oauthPoll.Body.String(), `"result_id"`) {
		t.Fatalf("second dummy oauth poll: status=%d body=%s", oauthPoll.Code, oauthPoll.Body.String())
	}

	createdResponse := request(http.MethodPost, "/api/providers", `{"name":"Dummy","provider":"dummy","config":{"name":"initial"}}`)
	if createdResponse.Code != http.StatusCreated || created == nil {
		t.Fatalf("create dummy provider: status=%d body=%s", createdResponse.Code, createdResponse.Body.String())
	}
	var account database.PersistedAccount
	accounts, err := db.ListAccounts()
	if err != nil || len(accounts) != 1 {
		t.Fatalf("accounts=%+v err=%v", accounts, err)
	}
	account = accounts[0]
	updated := request(http.MethodPut, "/api/providers/"+account.ID, `{"name":"Dummy Updated","provider":"dummy","config":{"name":"updated"}}`)
	if updated.Code != http.StatusOK || created.Config().Name != "updated" {
		t.Fatalf("update dummy provider: status=%d config=%+v body=%s", updated.Code, created.Config(), updated.Body.String())
	}
	key := request(http.MethodPost, "/api/keys", `{"name":"dummy-key","account_id":"`+account.ID+`"}`)
	if key.Code != http.StatusCreated || !strings.Contains(key.Body.String(), `"key"`) {
		t.Fatalf("create dummy API key: status=%d body=%s", key.Code, key.Body.String())
	}
	listed := request(http.MethodGet, "/api/providers", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), "Dummy Updated") {
		t.Fatalf("list dummy provider: status=%d body=%s", listed.Code, listed.Body.String())
	}
}
