package aiprovider

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func waitQueue(t *testing.T, a *Account, count int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		n := len(a.waiters)
		a.mu.Unlock()
		if n == count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("queue did not reach %d", count)
}
func TestAccountFIFOQueueFullAndCancellation(t *testing.T) {
	p, _ := NewDummyAIProvider("p", json.RawMessage(`{"enabled":true,"max_concurrent_connections":1}`))
	a := &Account{aiprovider: p}
	release, err := a.acquire(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan int, 2)
	releases := make(chan func(), 2)
	for i := 1; i <= 2; i++ {
		go func(n int) {
			release, err := a.acquire(context.Background(), 2)
			if err != nil {
				t.Error(err)
				return
			}
			acquired <- n
			releases <- release
		}(i)
		waitQueue(t, a, i)
	}
	if _, err := a.acquire(context.Background(), 2); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("queue full: %v", err)
	}
	release()
	if got := <-acquired; got != 1 {
		t.Fatalf("FIFO first=%d", got)
	}
	(<-releases)()
	if got := <-acquired; got != 2 {
		t.Fatalf("FIFO second=%d", got)
	}
	(<-releases)()
	release, _ = a.acquire(context.Background(), 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := a.acquire(ctx, 1); done <- err }()
	waitQueue(t, a, 1)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	waitQueue(t, a, 0)
	release()
	if a.active != 0 {
		t.Fatalf("leaked active slots: %d", a.active)
	}
}
func TestAccountConfigIncreaseAndDisableWakeWaiters(t *testing.T) {
	manager := NewAIProviderManager()
	_ = manager.Register("dummy", DummyAIProviderFactory(nil))
	_, err := manager.Create("p", "dummy", json.RawMessage(`{"enabled":true,"max_concurrent_connections":1}`))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := manager.GetAccount("p")
	release, _ := a.acquire(context.Background(), 100)
	granted := make(chan func(), 1)
	go func() {
		r, err := a.acquire(context.Background(), 100)
		if err != nil {
			t.Error(err)
		}
		granted <- r
	}()
	waitQueue(t, a, 1)
	if err := manager.UpdateConfig("p", json.RawMessage(`{"enabled":true,"max_concurrent_connections":2}`)); err != nil {
		t.Fatal(err)
	}
	release2 := <-granted
	waiting := make(chan error, 1)
	go func() { _, err := a.acquire(context.Background(), 100); waiting <- err }()
	waitQueue(t, a, 1)
	if err := manager.UpdateConfig("p", json.RawMessage(`{"enabled":false,"max_concurrent_connections":2}`)); err != nil {
		t.Fatal(err)
	}
	if err := <-waiting; !errors.Is(err, ErrUnavailable) {
		t.Fatalf("disabled waiter: %v", err)
	}
	release()
	release2()
	if a.active != 0 {
		t.Fatal("disabling leaked execution slots")
	}
	if _, err := a.acquire(context.Background(), 100); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("disabled account admitted request: %v", err)
	}
}
