package aiprovider2

import (
	"errors"
	"testing"
	"time"
)

func TestAccountLimiterClose(t *testing.T) {
	account := &Account{Config: AccountConfig{MaxConcurrentConnections: 1}}
	limiter := newAccountLimiter(account)
	if err := limiter.Acquire(t.Context()); err != nil {
		t.Fatal(err)
	}

	queued := make(chan error, 1)
	go func() {
		queued <- limiter.Acquire(t.Context())
	}()
	waitForQueuedConnection(t, account)

	limiter.Close()
	limiter.Close()
	select {
	case err := <-queued:
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("queued Acquire() error = %v, want %v", err, ErrUnavailable)
		}
	case <-time.After(time.Second):
		t.Fatal("queued Acquire() was not woken by Close()")
	}
	if err := limiter.Acquire(t.Context()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Acquire() after Close() error = %v, want %v", err, ErrUnavailable)
	}

	limiter.Release()
	account.mu.RLock()
	defer account.mu.RUnlock()
	if account.State.ActiveConnections != 0 || account.State.QueuedConnections != 0 {
		t.Fatalf("connection state = %+v, want zero counts", account.State)
	}
}

func waitForQueuedConnection(t *testing.T, account *Account) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		account.mu.RLock()
		queued := account.State.QueuedConnections
		account.mu.RUnlock()
		if queued == 1 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("Acquire() did not enter the queue")
}
