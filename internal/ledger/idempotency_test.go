package ledger

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/go-ledger/internal/testhelpers"
	"github.com/google/uuid"
)

func TestIdempotency_SequentialReplay(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	src, dst := testhelpers.NewFundedPair(t, pool, 100000)
	svc := New(pool)
	beforeTx := testhelpers.TransactionCount(t, pool)

	id1, replayed, err := svc.TransferIdempotent(context.Background(), ScopeTransfer, "key-seq-1", src, dst, 1000, "USD")
	if err != nil || replayed {
		t.Fatalf("first: id=%v replayed=%v err=%v", id1, replayed, err)
	}
	id2, replayed, err := svc.TransferIdempotent(context.Background(), ScopeTransfer, "key-seq-1", src, dst, 1000, "USD")
	if err != nil || !replayed || id1 != id2 {
		t.Fatalf("replay: id1=%v id2=%v replayed=%v err=%v", id1, id2, replayed, err)
	}
	if n := testhelpers.TransactionCount(t, pool); n != beforeTx+1 {
		t.Fatalf("tx count %d want %d", n, beforeTx+1)
	}
	if got := testhelpers.RawBalance(t, pool, src); got != 99000 {
		t.Fatalf("src %d want 99000 (double-post!)", got)
	}
	if drift := testhelpers.GlobalDrift(t, pool); drift != 0 {
		t.Fatalf("drift %d", drift)
	}
}

func TestIdempotency_ConcurrentReplay20(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	src, dst := testhelpers.NewFundedPair(t, pool, 100000)
	svc := New(pool)
	beforeTx := testhelpers.TransactionCount(t, pool)

	const n = 20
	ids := make([]uuid.UUID, n)
	var wg sync.WaitGroup
	var errCount atomic.Int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id, _, err := svc.TransferIdempotent(context.Background(), ScopeTransfer, "key-conc-1", src, dst, 1000, "USD")
			if err != nil {
				t.Logf("worker %d: %v", i, err)
				errCount.Add(1)
				return
			}
			ids[i] = id
		}(i)
	}
	wg.Wait()
	if errCount.Load() != 0 {
		t.Fatalf("%d workers errored", errCount.Load())
	}
	for i := 1; i < n; i++ {
		if ids[i] != ids[0] {
			t.Fatalf("id[%d]=%v != id[0]=%v", i, ids[i], ids[0])
		}
	}
	if n2 := testhelpers.TransactionCount(t, pool); n2 != beforeTx+1 {
		t.Fatalf("tx count %d want %d", n2, beforeTx+1)
	}
	if got := testhelpers.RawBalance(t, pool, src); got != 99000 {
		t.Fatalf("src %d want 99000", got)
	}
}

func TestIdempotency_SameKeyDifferentScope(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	src, dst := testhelpers.NewFundedPair(t, pool, 100000)
	svc := New(pool)
	beforeTx := testhelpers.TransactionCount(t, pool)

	if _, _, err := svc.TransferIdempotent(context.Background(), ScopeTransfer, "shared-key", src, dst, 1000, "USD"); err != nil {
		t.Fatalf("scope transfer: %v", err)
	}
	if _, _, err := svc.TransferIdempotent(context.Background(), "payment", "shared-key", src, dst, 1000, "USD"); err != nil {
		t.Fatalf("scope payment: %v", err)
	}
	if n := testhelpers.TransactionCount(t, pool); n != beforeTx+2 {
		t.Fatalf("tx count %d want %d (scopes must not collide)", n, beforeTx+2)
	}
}

func TestIdempotency_SameKeyDifferentUsers(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	testhelpers.TruncateAll(t, pool)
	sysUser, sysAcct := testhelpers.CreateSystemAccount(t, pool, "USD")
	_ = sysUser
	u1 := testhelpers.CreateUser(t, pool, "u1-"+uuid.NewString()+"@test.local")
	u2 := testhelpers.CreateUser(t, pool, "u2-"+uuid.NewString()+"@test.local")
	u3 := testhelpers.CreateUser(t, pool, "u3-"+uuid.NewString()+"@test.local")
	u4 := testhelpers.CreateUser(t, pool, "u4-"+uuid.NewString()+"@test.local")
	src1 := testhelpers.CreateAccount(t, pool, u1, "USD", "available")
	dst1 := testhelpers.CreateAccount(t, pool, u2, "USD", "available")
	src2 := testhelpers.CreateAccount(t, pool, u3, "USD", "available")
	dst2 := testhelpers.CreateAccount(t, pool, u4, "USD", "available")
	testhelpers.FundAccount(t, pool, sysAcct, src1, 50000, "USD")
	testhelpers.FundAccount(t, pool, sysAcct, src2, 50000, "USD")
	svc := New(pool)
	beforeTx := testhelpers.TransactionCount(t, pool)

	if _, _, err := svc.TransferIdempotent(context.Background(), ScopeTransfer, "same-client-uuid", src1, dst1, 1000, "USD"); err != nil {
		t.Fatalf("user1: %v", err)
	}
	if _, _, err := svc.TransferIdempotent(context.Background(), ScopeTransfer, "same-client-uuid", src2, dst2, 1000, "USD"); err != nil {
		t.Fatalf("user2: %v", err)
	}
	if n := testhelpers.TransactionCount(t, pool); n != beforeTx+2 {
		t.Fatalf("tx count %d want %d (users must not collide)", n, beforeTx+2)
	}
}

func TestIdempotency_DifferentKeysTwoRows(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	src, dst := testhelpers.NewFundedPair(t, pool, 100000)
	svc := New(pool)
	beforeTx := testhelpers.TransactionCount(t, pool)
	for _, k := range []string{"k-a", "k-b"} {
		if _, _, err := svc.TransferIdempotent(context.Background(), ScopeTransfer, k, src, dst, 1000, "USD"); err != nil {
			t.Fatalf("key %s: %v", k, err)
		}
	}
	if n := testhelpers.TransactionCount(t, pool); n != beforeTx+2 {
		t.Fatalf("tx count %d want %d", n, beforeTx+2)
	}
}

func TestIdempotency_HashMismatch422(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	src, dst := testhelpers.NewFundedPair(t, pool, 100000)
	svc := New(pool)
	if _, _, err := svc.TransferIdempotent(context.Background(), ScopeTransfer, "key-mismatch", src, dst, 1000, "USD"); err != nil {
		t.Fatalf("first: %v", err)
	}
	_, _, err := svc.TransferIdempotent(context.Background(), ScopeTransfer, "key-mismatch", src, dst, 2000, "USD")
	if !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatalf("want ErrIdempotencyMismatch got %v", err)
	}

	if got := testhelpers.RawBalance(t, pool, src); got != 99000 {
		t.Fatalf("src %d want 99000", got)
	}
}
