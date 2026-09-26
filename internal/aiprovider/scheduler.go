package aiprovider

import (
	"net/http"
)

type GroupScheduler interface {
	GetAccount(req *http.Request) *Account
	NotifyConfigChangedLocked()
}

type groupScheduler struct {
	groupAccount  *Account
	configChanged chan struct{}
}

func newGroupScheduler(groupAccount *Account) GroupScheduler {
	if groupAccount == nil {
		groupAccount = &Account{}
	}
	groupAccount.mu.Lock()
	scheduler := groupAccount.scheduler
	if scheduler == nil {
		scheduler = &groupScheduler{
			groupAccount:  groupAccount,
			configChanged: make(chan struct{}),
		}
		groupAccount.scheduler = scheduler
	}
	groupAccount.mu.Unlock()
	return scheduler
}

func (s *groupScheduler) GetAccount(_ *http.Request) *Account {
	if s == nil || s.groupAccount == nil || s.groupAccount.manager == nil {
		return nil
	}

	s.groupAccount.mu.RLock()
	if len(s.groupAccount.Config.Members) == 0 {
		s.groupAccount.mu.RUnlock()
		return nil
	}
	accountID := s.groupAccount.Config.Members[0].ID
	s.groupAccount.mu.RUnlock()

	return s.groupAccount.manager.getAccount(accountID)
}

func (s *groupScheduler) NotifyConfigChangedLocked() {
	if s.configChanged != nil {
		close(s.configChanged)
	}
	s.configChanged = make(chan struct{})
}
