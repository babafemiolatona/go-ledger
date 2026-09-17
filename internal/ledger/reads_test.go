package ledger

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/go-ledger/internal/testhelpers"
)

func TestReads_ListAccountsAndBalance(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	testhelpers.TruncateAll(t, pool)
	u1 := testhelpers.CreateUser(t, pool, "m4-u1-"+uuid.NewString()+"@test.local")
	u2 := testhelpers.CreateUser(t, pool, "m4-u2-"+uuid.NewString()+"@test.local")
	sysUser, sysAcct := testhelpers.CreateSystemAccount(t, pool, "USD")
	_ = sysUser
	a1 := testhelpers.CreateAccount(t, pool, u1, "USD", "available")
	a2 := testhelpers.CreateAccount(t, pool, u1, "EUR", "available")
	_ = testhelpers.CreateAccount(t, pool, u2, "USD", "available")
	testhelpers.FundAccount(t, pool, sysAcct, a1, 50000, "USD")
	svc := New(pool)

	acs, err := svc.ListAccounts(context.Background(), u1)
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(acs) != 2 {
		t.Fatalf("want 2 accounts for u1, got %d", len(acs))
	}

	acs2, _ := svc.ListAccounts(context.Background(), u2)
	if len(acs2) != 1 {
		t.Fatalf("want 1 for u2, got %d", len(acs2))
	}
	empty, _ := svc.ListAccounts(context.Background(), uuid.New())
	if len(empty) != 0 {
		t.Fatalf("want 0 for unknown user")
	}

	bal, err := svc.GetBalance(context.Background(), a1)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if bal.Posted != 50000 {
		t.Fatalf("posted %d want 50000", bal.Posted)
	}
	if bal.Available != 50000 {
		t.Fatalf("available %d want 50000", bal.Available)
	}
	if bal.Pending != 0 {
		t.Fatalf("pending %d want 0", bal.Pending)
	}

	_, err = svc.Transfer(context.Background(), a1, acs2[0].ID, 10000, "USD")
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	bal2, _ := svc.GetBalance(context.Background(), a1)
	if bal2.Posted != 40000 {
		t.Fatalf("after transfer posted %d want 40000", bal2.Posted)
	}
	_ = a2
}

func TestReads_StatementRunningBalanceAndCounterparty(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	src, dst := testhelpers.NewFundedPair(t, pool, 100000)
	svc := New(pool)
	var txIDs []uuid.UUID
	for i := 0; i < 5; i++ {
		txID, err := svc.Transfer(context.Background(), src, dst, 1000, "USD")
		if err != nil {
			t.Fatalf("transfer %d: %v", i, err)
		}
		txIDs = append(txIDs, txID)
	}
	page, err := svc.GetStatement(context.Background(), src, 0, 100)
	if err != nil {
		t.Fatalf("GetStatement: %v", err)
	}
	// NewFundedPair seeds a deposit (credit) plus 5 transfers, so total 6 entries for src
	if len(page.Entries) != 6 {
		t.Fatalf("entries %d want 6 (1 deposit + 5 transfers)", len(page.Entries))
	}
	bal, _ := svc.GetBalance(context.Background(), src)
	lastRB := page.Entries[len(page.Entries)-1].RunningBalance
	if lastRB != bal.Posted {
		t.Fatalf("last running_balance %d != posted %d", lastRB, bal.Posted)
	}
	// filter transfer entries only — deposit has system as counterparty
	var xfers []StatementEntry
	for _, e := range page.Entries {
		if e.TransactionType == "transfer" {
			xfers = append(xfers, e)
		}
	}
	if len(xfers) != 5 {
		t.Fatalf("transfer entries %d want 5", len(xfers))
	}
	for i, e := range xfers {
		if e.CounterpartyAccountID == nil || *e.CounterpartyAccountID != dst {
			t.Fatalf("row %d counterparty %v want %s", i, e.CounterpartyAccountID, dst)
		}
		if e.Direction != "debit" {
			t.Fatalf("src direction %q want debit", e.Direction)
		}
	}
	det, err := svc.GetTransaction(context.Background(), txIDs[0])
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if len(det.Entries) != 2 {
		t.Fatalf("tx entries %d want 2", len(det.Entries))
	}
	if det.Type != "transfer" {
		t.Fatalf("tx type %q", det.Type)
	}
}

func TestReads_StatementPaginationNoGapsDupes(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	src, dst := testhelpers.NewFundedPair(t, pool, 200000)
	svc := New(pool)
	for i := 0; i < 30; i++ {
		if _, err := svc.Transfer(context.Background(), src, dst, 1000, "USD"); err != nil {
			t.Fatalf("seed transfer %d: %v", i, err)
		}
	}
	var seen []int64
	var cursor int64
	for {
		page, err := svc.GetStatement(context.Background(), src, cursor, 5)
		if err != nil {
			t.Fatalf("page cursor %d: %v", cursor, err)
		}
		for _, e := range page.Entries {
			seen = append(seen, e.ID)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	// 30 transfers + 1 seed deposit = 31 rows for src
	if len(seen) != 31 {
		t.Fatalf("paginated count %d want 31 (30 transfers + 1 deposit)", len(seen))
	}
	m := map[int64]int{}
	for _, id := range seen {
		m[id]++
	}
	for id, c := range m {
		if c != 1 {
			t.Fatalf("dupe id %d count %d", id, c)
		}
	}
	for i := 1; i < len(seen); i++ {
		if seen[i] <= seen[i-1] {
			t.Fatalf("not increasing %d -> %d at %d", seen[i-1], seen[i], i)
		}
	}
}

func TestReads_StatementUnderConcurrentWrites(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	src, dst := testhelpers.NewFundedPair(t, pool, 500000)
	svc := New(pool)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = svc.Transfer(context.Background(), src, dst, 1000, "USD") }()
	}
	for i := 0; i < 5; i++ {
		_, _ = svc.GetStatement(context.Background(), src, 0, 100)
	}
	wg.Wait()
	page, _ := svc.GetStatement(context.Background(), src, 0, 1000)
	bal, _ := svc.GetBalance(context.Background(), src)
	if len(page.Entries) == 0 {
		t.Fatal("no entries after concurrent writes")
	}
	if page.Entries[len(page.Entries)-1].RunningBalance != bal.Posted {
		t.Fatalf("concurrent: last RB %d != posted %d", page.Entries[len(page.Entries)-1].RunningBalance, bal.Posted)
	}
	if drift := testhelpers.GlobalDrift(t, pool); drift != 0 {
		t.Fatalf("drift %d want 0", drift)
	}
}
