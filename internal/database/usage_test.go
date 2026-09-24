package database

import (
	"testing"
	"time"
)

func TestQueryAccountAndUserUsage(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	rangeOK := TimeRange{Start: now.Add(-time.Hour), End: now.Add(time.Hour)}

	user := &PersistedUser{Name: "alice", Role: UserRoleUser, PasswordHash: "x", Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err := db.SaveUser(user); err != nil {
		t.Fatal(err)
	}
	accountA := &PersistedAccount{AIProvider: "dummy", Name: "A", Config: []byte(`{}`), CreatedAt: now, UpdatedAt: now}
	accountB := &PersistedAccount{AIProvider: "api", Name: "B", Config: []byte(`{}`), CreatedAt: now, UpdatedAt: now}
	if err := db.SaveAccount(accountA); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAccount(accountB); err != nil {
		t.Fatal(err)
	}
	key := &PersistedAPIKey{UserID: user.ID, AccountID: accountA.ID, Name: "k", Key: "sk-alice", CreatedAt: now, UpdatedAt: now}
	if err := db.SaveAPIKey(key); err != nil {
		t.Fatal(err)
	}

	traces := []*PersistedCallTrace{
		{APIKey: "sk-alice", AccountID: accountA.ID, AIProviderType: "dummy", InputTokens: 10, OutputTokens: 2, FinishedAt: now},
		{APIKey: "sk-alice", AccountID: accountA.ID, AIProviderType: "dummy", InputTokens: 5, OutputTokens: 1, CacheReadTokens: 3, FinishedAt: now},
		{APIKey: "sk-unknown", AccountID: accountB.ID, AIProviderType: "api", InputTokens: 7, OutputTokens: 4, FinishedAt: now},
	}
	for _, tr := range traces {
		if err := db.RecordCallTrace(tr); err != nil {
			t.Fatal(err)
		}
	}

	accountRows, accountTotals, err := db.QueryAccountUsage(rangeOK)
	if err != nil {
		t.Fatal(err)
	}
	if len(accountRows) != 2 || accountTotals.Requests != 3 {
		t.Fatalf("account usage: rows=%+v totals=%+v", accountRows, accountTotals)
	}
	byAccount := map[int]UsageTotals{}
	for _, row := range accountRows {
		byAccount[row.AccountID] = row.Usage
	}
	if byAccount[accountA.ID].Requests != 2 || byAccount[accountA.ID].InputTokens != 15 || byAccount[accountA.ID].OutputTokens != 3 || byAccount[accountA.ID].CacheReadTokens != 3 {
		t.Fatalf("account A: %+v", byAccount[accountA.ID])
	}
	if byAccount[accountA.ID].TotalTokens != 15+3+3 {
		t.Fatalf("account A total tokens: %+v", byAccount[accountA.ID])
	}
	if byAccount[accountB.ID].Requests != 1 || byAccount[accountB.ID].InputTokens != 7 {
		t.Fatalf("account B: %+v", byAccount[accountB.ID])
	}

	userRows, userTotals, err := db.QueryUserUsage(rangeOK, 0)
	if err != nil {
		t.Fatal(err)
	}
	if userTotals.Requests != 3 {
		t.Fatalf("user totals: %+v", userTotals)
	}
	byUser := map[int]UsageTotals{}
	for _, row := range userRows {
		byUser[row.UserID] = row.Usage
	}
	if byUser[user.ID].Requests != 2 || byUser[user.ID].InputTokens != 15 {
		t.Fatalf("alice usage: %+v", byUser[user.ID])
	}
	if byUser[0].Requests != 1 || byUser[0].InputTokens != 7 {
		t.Fatalf("unattributed usage: %+v", byUser[0])
	}

	filtered, filteredTotals, err := db.QueryUserUsage(rangeOK, accountA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if filteredTotals.Requests != 2 || len(filtered) != 1 || filtered[0].UserID != user.ID {
		t.Fatalf("filtered user usage: rows=%+v totals=%+v", filtered, filteredTotals)
	}

	empty, emptyTotals, err := db.QueryAccountUsage(TimeRange{Start: now.Add(-48 * time.Hour), End: now.Add(-47 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 || emptyTotals.Requests != 0 {
		t.Fatalf("expected empty range, got %+v %+v", empty, emptyTotals)
	}
	if _, _, err := db.QueryAccountUsage(TimeRange{}); err == nil {
		t.Fatal("empty time range accepted")
	}
}
