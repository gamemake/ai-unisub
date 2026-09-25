package aiprovider2

import (
	"encoding/json"
	"testing"
)

func TestGroupSchedulerAlwaysReturnsFirstAccount(t *testing.T) {
	first := &Account{ID: 1}
	second := &Account{ID: 2}
	manager := &providerManager{
		accounts: map[int]*Account{
			first.ID:  first,
			second.ID: second,
		},
	}
	group := &Account{
		manager: manager,
		Config: AccountConfig{
			Kind: AccountGroup,
			Members: []GroupMember{
				{ID: first.ID, Weight: 1},
				{ID: second.ID, Weight: 1},
			},
		},
	}

	scheduler := newGroupScheduler(group)
	for range 3 {
		if got := scheduler.GetAccount(nil); got != first {
			t.Fatalf("GetAccount() = %p, want first account %p", got, first)
		}
	}
}

func TestGroupSchedulerReturnsNilWithoutFirstAccount(t *testing.T) {
	manager := &providerManager{accounts: make(map[int]*Account)}
	group := &Account{manager: manager, Config: AccountConfig{Kind: AccountGroup}}
	scheduler := newGroupScheduler(group)

	if got := scheduler.GetAccount(nil); got != nil {
		t.Fatalf("GetAccount() = %p, want nil", got)
	}
}

func TestGroupSchedulerReceivesConfigChanges(t *testing.T) {
	manager := &providerManager{accounts: make(map[int]*Account)}
	group := &Account{
		manager: manager,
		ID:      10,
		Config: AccountConfig{
			Kind:    AccountGroup,
			Name:    "group",
			Members: []GroupMember{{ID: 1, Weight: 1}},
		},
	}
	scheduler := newGroupScheduler(group).(*groupScheduler)
	configChanged := scheduler.configChanged

	err := group.UpdateConfig(json.RawMessage(`{
		"kind":"group",
		"name":"updated group",
		"members":[{"id":2,"weight":1}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-configChanged:
	default:
		t.Fatal("scheduler was not notified of the account config change")
	}
}
