package ledger

import (
	"context"
	"testing"

	"github.com/go-ledger/internal/testhelpers"
)

func TestReconcile_ZeroDrift(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	testhelpers.TruncateAll(t, pool)
	src, dst := testhelpers.NewFundedPair(t, pool, 100000)
	svc := New(pool)
	svc.Transfer(context.Background(), src, dst, 1000, "USD")
	res, err := svc.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.GlobalDrift != 0 || res.Status != "ok" {
		t.Fatalf("drift %d status %s", res.GlobalDrift, res.Status)
	}
	if len(res.PerTxDrift) != 0 {
		t.Fatalf("perTx drift %v", res.PerTxDrift)
	}
}

func TestReconcile_DetectsUnbalancedFixture(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	testhelpers.TruncateAll(t, pool)
	src, dst := testhelpers.NewFundedPair(t, pool, 100000)
	testhelpers.InsertUnbalancedEntry(t, pool, src, dst, 999, "USD")
	svc := New(pool)
	res, _ := svc.Reconcile(context.Background())
	if res.GlobalDrift == 0 {
		t.Fatalf("should detect drift")
	}
	if len(res.PerTxDrift) == 0 {
		t.Fatalf("perTx should have drift")
	}
}
