package aiprovider

import (
	"encoding/json"
	"errors"
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
	manager.accounts[2] = &Account{manager: manager, ID: 2, Config: AccountConfig{Kind: AccountAPI}}
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

func TestGroupNestingRejected(t *testing.T) {
	manager := &providerManager{accounts: make(map[int]*Account)}
	add := func(id int, config AccountConfig) *Account {
		account := &Account{manager: manager, ID: id, Config: config}
		manager.accounts[id] = account
		return account
	}
	member := add(1, AccountConfig{Kind: AccountAPI})
	group := add(2, AccountConfig{Kind: AccountGroup, Members: []GroupMember{{ID: 1, Weight: 1}}})

	for _, tc := range []struct {
		name    string
		account *Account
		config  AccountConfig
		want    error
	}{
		{"group member is group", add(3, AccountConfig{Kind: AccountAPI}), AccountConfig{Kind: AccountGroup, Members: []GroupMember{{ID: 2, Weight: 1}}}, errNestedGroupMember},
		{"group member missing", group, AccountConfig{Kind: AccountGroup, Members: []GroupMember{{ID: 99, Weight: 1}}}, ErrAccountNotFound},
		{"member becomes group", member, AccountConfig{Kind: AccountGroup, Members: []GroupMember{{ID: 3, Weight: 1}}}, errGroupMemberCannotBeGroup},
		{"valid group", group, AccountConfig{Kind: AccountGroup, Members: []GroupMember{{ID: 1, Weight: 1}, {ID: 3, Weight: 1}}}, nil},
	} {
		if err := tc.account.checkGroupNesting(tc.config); !errors.Is(err, tc.want) {
			t.Errorf("%s: checkGroupNesting() = %v, want %v", tc.name, err, tc.want)
		}
	}
}
