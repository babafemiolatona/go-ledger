package ledger

import (
	"context"
	"testing"
	"time"

	"github.com/go-ledger/internal/testhelpers"
)

func TestSweeper_ExpiresHold(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	testhelpers.TruncateAll(t, pool)
	svc := New(pool)
	src, esc := newHoldPair(t, svc, 100000)
	holdID, _ := svc.Hold(context.Background(), src, esc, 10000, "USD", time.Now().Add(time.Hour))
	pool.Exec(context.Background(), `UPDATE transactions SET expires_at = now() - interval '1 second' WHERE id=$1`, holdID)
	n, _ := svc.SweepExpiredHolds(context.Background())
	if n != 1 {
		t.Fatalf("swept %d want 1", n)
	}
	bal, _ := svc.GetBalance(context.Background(), src)
	if bal.Pending != 0 {
		t.Fatalf("pending after sweep %d want 0", bal.Pending)
	}
}
