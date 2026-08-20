package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/config"
	"github.com/ai-unisub/ai-unisub/internal/model"
)

func TestQueueTimeoutUsesAccountThenEnvironmentThenBuiltInDefault(t *testing.T) {
	server := &Server{cfg: config.Config{ConcurrencyQueueTimeout: 45 * time.Second}}
	if got := server.queueTimeout(model.Account{}); got != 45*time.Second {
		t.Fatalf("inherited queue timeout = %s", got)
	}
	if got := server.queueTimeout(model.Account{ConcurrencyQueueTimeoutSeconds: 25}); got != 25*time.Second {
		t.Fatalf("account queue timeout = %s", got)
	}

	application, _ := testServer(t, "https://example.invalid/responses")
	if got := application.queueTimeout(model.Account{}); got != 3*time.Minute {
		t.Fatalf("built-in queue timeout = %s", got)
	}
}

func TestAcquireWaitsForAccountSlot(t *testing.T) {
	server := &Server{}
	releaseFirst, err := server.acquire(context.Background(), 1, 1, 0)
	if err != nil {
		t.Fatal(err)
	}

	acquired := make(chan error, 1)
	go func() {
		releaseSecond, err := server.acquire(context.Background(), 1, 1, time.Second)
		if err == nil {
			releaseSecond()
		}
		acquired <- err
	}()

	select {
	case err := <-acquired:
		t.Fatalf("waiter completed before a slot was released: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	releaseFirst()
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter did not acquire the released slot")
	}
}

func TestAcquireQueueTimeoutAndCancellation(t *testing.T) {
	server := &Server{}
	release, err := server.acquire(context.Background(), 2, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	started := time.Now()
	_, err = server.acquire(context.Background(), 2, 1, 30*time.Millisecond)
	if !errors.Is(err, errConcurrencyQueueTimeout) {
		t.Fatalf("queue timeout error = %v", err)
	}
	if time.Since(started) < 20*time.Millisecond {
		t.Fatal("queue timeout returned too early")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = server.acquire(ctx, 2, 1, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
}
