package oauth2

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

// DummyAdapter 是仅用于开发和测试、不发送网络请求的设备授权适配器。
type DummyAdapter struct {
	mu    sync.Mutex
	polls map[string]int
}

func NewDummyAdapter() *DummyAdapter {
	return &DummyAdapter{polls: make(map[string]int)}
}

func (a *DummyAdapter) Service() string { return OAuthServiceDummy }

func (a *DummyAdapter) Refresh(_ context.Context, credential *OAuthCredential, _ *http.Client) (*OAuthCredential, error) {
	if credential == nil {
		return nil, errors.New("credential is nil")
	}
	result := *credential
	return &result, nil
}

func (a *DummyAdapter) StartDeviceAuthorization(_ context.Context, _ DeviceStartInput) (DeviceAuthorizationResult, error) {
	id, err := randomID()
	if err != nil {
		return DeviceAuthorizationResult{}, err
	}
	deviceCode := "dummy-" + id
	a.mu.Lock()
	a.polls[deviceCode] = 0
	a.mu.Unlock()
	return DeviceAuthorizationResult{
		DeviceCode: deviceCode, UserCode: "DUMMY-CODE", VerificationURI: "#dummy-oauth",
		ExpiresAt: time.Now().Add(oauthSessionTTL), Interval: time.Second,
	}, nil
}

func (a *DummyAdapter) PollDeviceToken(_ context.Context, deviceCode string, _ *http.Client) (*OAuthCredential, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	polls, ok := a.polls[deviceCode]
	if !ok {
		return nil, errors.New("dummy device code not found")
	}
	if polls == 0 {
		a.polls[deviceCode] = 1
		return nil, ErrAuthorizationPending
	}
	delete(a.polls, deviceCode)
	return &OAuthCredential{
		AccessToken: "dummy-access-token", RefreshToken: "dummy-refresh-token", TokenType: "Bearer",
		ExpiresAt: time.Now().Add(time.Hour), AccountID: "dummy-account",
		AccountName: "Dummy OAuth User", Email: "dummy@example.test",
	}, nil
}
