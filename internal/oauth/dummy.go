package oauth

import (
	"context"
	"sync"
	"time"
)

// DummyAdapter is a deterministic, local OAuth adapter intended for demos and
// development environments. It never makes network requests.
type DummyAdapter struct {
	mu    sync.Mutex
	polls map[string]int
}

func NewDummyAdapter() *DummyAdapter    { return &DummyAdapter{polls: make(map[string]int)} }
func (a *DummyAdapter) Service() string { return "dummy" }
func (a *DummyAdapter) Refresh(_ context.Context, credential *OAuthCredential) (*OAuthCredential, error) {
	return credential, nil
}
func (a *DummyAdapter) StartDeviceAuthorization(_ context.Context, _ DeviceStartInput) (DeviceAuthorizationResult, error) {
	return DeviceAuthorizationResult{DeviceCode: "dummy-device-code", UserCode: "DUMMY-CODE", VerificationURI: "#dummy-oauth", ExpiresAt: time.Now().Add(10 * time.Minute), Interval: time.Second}, nil
}
func (a *DummyAdapter) PollDeviceToken(_ context.Context, deviceCode string) (*OAuthCredential, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.polls[deviceCode]++
	if a.polls[deviceCode] == 1 {
		return nil, ErrAuthorizationPending
	}
	return &OAuthCredential{AccessToken: "dummy-access-token", RefreshToken: "dummy-refresh-token", TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour), AccountID: "dummy-account", AccountName: "Dummy OAuth User", Email: "dummy@example.test"}, nil
}
