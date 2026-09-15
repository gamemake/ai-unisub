package aiprovider

import (
	"ai-unisub/internal/common"
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

var ErrQueueFull = errors.New(common.MessageAIProviderQueueFull)
var ErrQueueTimeout = errors.New(common.MessageAIProviderQueueTimeout)
var ErrUnavailable = errors.New(common.MessageAIProviderUnavailable)

// Account owns one provider's execution slots and FIFO queue. Waiting requests
// keep their original context, and cancellation never starts an upstream call.
type Account struct {
	mu         sync.Mutex
	aiprovider AIProvider
	active     int
	waiters    []*accountWaiter
	closed     bool
}
type accountWaiter struct {
	ready       chan struct{}
	granted     bool
	unavailable bool
}

func (a *Account) wake() {
	if a.closed || !a.aiprovider.Config().Enabled {
		for _, ready := range a.waiters {
			ready.unavailable = true
			close(ready.ready)
		}
		a.waiters = nil
		return
	}
	limit := max(1, a.aiprovider.Config().MaxConcurrentConnections)
	for len(a.waiters) > 0 && a.active < limit {
		next := a.waiters[0]
		a.waiters = a.waiters[1:]
		a.active++
		next.granted = true
		close(next.ready)
	}
}
func (a *Account) acquire(ctx context.Context, queueLimit int) (func(), error) {
	a.mu.Lock()
	if err := ctx.Err(); err != nil {
		a.mu.Unlock()
		return nil, err
	}
	config := a.aiprovider.Config()
	if a.closed || !config.Enabled {
		a.mu.Unlock()
		return nil, ErrUnavailable
	}
	if a.active < max(1, config.MaxConcurrentConnections) && len(a.waiters) == 0 {
		a.active++
		a.mu.Unlock()
		return a.release, nil
	}
	if queueLimit <= 0 {
		queueLimit = 100
	}
	if len(a.waiters) >= queueLimit {
		a.mu.Unlock()
		return nil, ErrQueueFull
	}
	ready := &accountWaiter{ready: make(chan struct{})}
	a.waiters = append(a.waiters, ready)
	timeout := config.QueueTimeoutSeconds
	if timeout <= 0 {
		timeout = 180
	}
	a.mu.Unlock()
	timer := time.NewTimer(time.Duration(timeout) * time.Second)
	defer timer.Stop()
	var err error
	select {
	case <-ready.ready:
		a.mu.Lock()
		unavailable := ready.unavailable || a.closed || !a.aiprovider.Config().Enabled
		if unavailable && ready.granted {
			a.active--
			a.wake()
		}
		a.mu.Unlock()
		if unavailable {
			return nil, ErrUnavailable
		}
		if err := ctx.Err(); err != nil {
			a.release()
			return nil, err
		}
		return a.release, nil
	case <-ctx.Done():
		err = ctx.Err()
	case <-timer.C:
		err = ErrQueueTimeout
	}
	a.mu.Lock()
	for i, waiter := range a.waiters {
		if waiter == ready {
			a.waiters = append(a.waiters[:i], a.waiters[i+1:]...)
			break
		}
	}
	// A release may have granted a slot concurrently with cancellation.
	if ready.granted {
		a.active--
	}
	a.wake()
	a.mu.Unlock()
	return nil, err
}
func (a *Account) release() { a.mu.Lock(); a.active--; a.wake(); a.mu.Unlock() }
func (a *Account) Handle(r *http.Request, recorder APICallRecorder, queueLimit int) error {
	release, err := a.acquire(r.Context(), queueLimit)
	if err != nil {
		return err
	}
	defer release()
	if !AllowsClient(a.aiprovider.Config(), DetectClient(r.Header)) {
		return ErrClientDenied
	}
	a.aiprovider.Handle(r, recorder)
	return nil
}
