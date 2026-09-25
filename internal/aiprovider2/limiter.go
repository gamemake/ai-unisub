package aiprovider2

import (
	"ai-unisub/internal/common"
	"context"
	"errors"
	"time"
)

const defaultQueueTimeout = 180 * time.Second

var ErrQueueTimeout = errors.New(common.MessageAIProviderQueueTimeout)
var ErrUnavailable = errors.New(common.MessageAIProviderUnavailable)

type AccountLimiter interface {
	Acquire(ctx context.Context) error
	Release()
	Close()
	NotifyConfigChangedLocked()
}

type accountLimiter struct {
	account       *Account
	available     chan struct{}
	configChanged chan struct{}
	closedSignal  chan struct{}
	closed        bool
}

func newAccountLimiter(account *Account) AccountLimiter {
	if account == nil {
		account = &Account{}
	}
	account.mu.Lock()
	limiter := account.limiter
	if limiter == nil {
		limiter = &accountLimiter{
			account:       account,
			available:     make(chan struct{}),
			configChanged: make(chan struct{}),
			closedSignal:  make(chan struct{}),
		}
		account.limiter = limiter
	}
	account.mu.Unlock()

	return limiter
}

func (l *accountLimiter) Acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	l.account.mu.Lock()
	if l.closed {
		l.account.mu.Unlock()
		return ErrUnavailable
	}
	if l.account.State.QueuedConnections == 0 && l.canAcquireLocked() {
		l.account.State.ActiveConnections++
		l.account.Dirty = true
		l.account.mu.Unlock()
		return nil
	}
	l.account.State.QueuedConnections++
	l.account.Dirty = true
	queuedAt := time.Now()

	for {
		if l.closed {
			l.account.State.QueuedConnections--
			l.account.Dirty = true
			l.account.mu.Unlock()
			return ErrUnavailable
		}
		remaining := l.queueTimeoutLocked() - time.Since(queuedAt)
		if remaining <= 0 {
			l.account.State.QueuedConnections--
			l.account.Dirty = true
			l.account.mu.Unlock()
			return ErrQueueTimeout
		}
		available := l.available
		configChanged := l.configChanged
		closedSignal := l.closedSignal
		l.account.mu.Unlock()

		timer := time.NewTimer(remaining)
		var waitErr error
		select {
		case <-available:
		case <-configChanged:
		case <-closedSignal:
		case <-ctx.Done():
			waitErr = ctx.Err()
		case <-timer.C:
			waitErr = ErrQueueTimeout
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}

		l.account.mu.Lock()
		if l.closed {
			l.account.State.QueuedConnections--
			l.account.Dirty = true
			l.account.mu.Unlock()
			return ErrUnavailable
		}
		if waitErr == nil {
			waitErr = ctx.Err()
		}
		if waitErr != nil {
			l.account.State.QueuedConnections--
			l.account.Dirty = true
			l.account.mu.Unlock()
			return waitErr
		}
		if l.canAcquireLocked() {
			l.account.State.QueuedConnections--
			l.account.State.ActiveConnections++
			l.account.Dirty = true
			l.account.mu.Unlock()
			return nil
		}
	}
}

func (l *accountLimiter) Release() {
	l.account.mu.Lock()
	if l.account.State.ActiveConnections > 0 {
		l.account.State.ActiveConnections--
		l.account.Dirty = true
	}
	close(l.available)
	l.available = make(chan struct{})
	l.account.mu.Unlock()
}

func (l *accountLimiter) Close() {
	l.account.mu.Lock()
	if !l.closed {
		l.closed = true
		close(l.closedSignal)
	}
	l.account.mu.Unlock()
}

func (l *accountLimiter) NotifyConfigChangedLocked() {
	close(l.configChanged)
	l.configChanged = make(chan struct{})
}

func (l *accountLimiter) canAcquireLocked() bool {
	return l.account.State.ActiveConnections < max(1, l.account.Config.MaxConcurrentConnections)
}

func (l *accountLimiter) queueTimeoutLocked() time.Duration {
	if l.account.Config.QueueTimeoutSeconds <= 0 {
		return defaultQueueTimeout
	}
	return time.Duration(l.account.Config.QueueTimeoutSeconds) * time.Second
}
