package oauth

import (
	"context"
	"errors"
	"testing"
	"time"
)

type dummyDeviceAdapter struct {
	polls int
}

func (a *dummyDeviceAdapter) Service() string { return "dummy-device" }
func (a *dummyDeviceAdapter) Refresh(_ context.Context, credential *OAuthCredential) (*OAuthCredential, error) {
	return credential, nil
}
func (a *dummyDeviceAdapter) StartDeviceAuthorization(context.Context, DeviceStartInput) (DeviceAuthorizationResult, error) {
	return DeviceAuthorizationResult{
		DeviceCode:      "dummy-device-code",
		UserCode:        "DUMMY-CODE",
		VerificationURI: "https://dummy.test/authorize",
		ExpiresAt:       time.Now().Add(time.Minute),
		Interval:        time.Second,
	}, nil
}
func (a *dummyDeviceAdapter) PollDeviceToken(context.Context, string) (*OAuthCredential, error) {
	a.polls++
	if a.polls == 1 {
		return nil, ErrAuthorizationPending
	}
	return &OAuthCredential{
		AccessToken:  "dummy-access-token",
		RefreshToken: "dummy-refresh-token",
		AccountID:    "dummy-account",
	}, nil
}

func TestDummyDeviceOAuthFlow(t *testing.T) {
	adapter := &dummyDeviceAdapter{}
	manager := NewManager(nil)
	if err := manager.Register(adapter); err != nil {
		t.Fatal(err)
	}
	started, err := manager.Start(context.Background(), adapter.Service(), "user-1", "http://dummy.test/callback")
	if err != nil {
		t.Fatal(err)
	}
	if started.UserCode != "DUMMY-CODE" || started.VerificationURI == "" {
		t.Fatalf("unexpected dummy start result: %+v", started)
	}

	if _, err := manager.Poll(context.Background(), started.SessionID); !errors.Is(err, ErrAuthorizationPending) {
		t.Fatalf("first poll error=%v, want pending", err)
	}
	credential, err := manager.Poll(context.Background(), started.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if credential.AccessToken != "dummy-access-token" {
		t.Fatalf("unexpected dummy credential: %+v", credential)
	}
	if _, err := manager.Poll(context.Background(), started.SessionID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("replay poll error=%v, want session not found", err)
	}
}
