package ledger

import (
	"context"
	"testing"
	"time"

	"github.com/go-ledger/internal/testhelpers"
	"github.com/google/uuid"
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

func holdEntryCount(t *testing.T, svc *Service, holdID uuid.UUID) int {
	t.Helper()
	var n int
	if err := svc.Pool().QueryRow(context.Background(),
		`SELECT count(*) FROM ledger_entries WHERE transaction_id=$1`, holdID).Scan(&n); err != nil {
		t.Fatalf("entry count: %v", err)
	}
	return n
}

func TestSweeper_DoesNotReprocessAlreadyReleasedHold(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	testhelpers.TruncateAll(t, pool)
	svc := New(pool)
	ctx := context.Background()

	src, esc := newHoldPair(t, svc, 100000)
	holdID, err := svc.Hold(ctx, src, esc, 10000, "USD", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Hold: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE transactions SET expires_at = now() - interval '1 second' WHERE id=$1`, holdID); err != nil {
		t.Fatalf("expire: %v", err)
	}

	if n, err := svc.SweepExpiredHolds(ctx); err != nil || n != 1 {
		t.Fatalf("first sweep n=%d err=%v want 1", n, err)
	}
	// Hold (2 pending legs) + Release (2 reversing pending legs).
	if got := holdEntryCount(t, svc, holdID); got != 4 {
		t.Fatalf("entries after release %d want 4", got)
	}

	// Same expired hold again: the reversal legs make the sweeper skip it
	// (more than one pending credit leg), so nothing new may be written.
	if n, err := svc.SweepExpiredHolds(ctx); err != nil || n != 0 {
		t.Fatalf("second sweep n=%d err=%v want 0", n, err)
	}
	if got := holdEntryCount(t, svc, holdID); got != 4 {
		t.Fatalf("entries after re-sweep %d want 4 (hold reprocessed!)", got)
	}
	if d := testhelpers.GlobalDrift(t, pool); d != 0 {
		t.Fatalf("global drift %d want 0", d)
	}
}

func TestSweeper_SkipsAlreadyCapturedHold(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	testhelpers.TruncateAll(t, pool)
	svc := New(pool)
	ctx := context.Background()

	src, esc := newHoldPair(t, svc, 100000)
	holdID, err := svc.Hold(ctx, src, esc, 10000, "USD", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Hold: %v", err)
	}
	// Capture BEFORE expiry (Capture rejects expired holds).
	if err := svc.Capture(ctx, holdID); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	// Hold (2 pending legs) + Capture (2 posted legs).
	if got := holdEntryCount(t, svc, holdID); got != 4 {
		t.Fatalf("entries after capture %d want 4", got)
	}
	if _, err := pool.Exec(ctx, `UPDATE transactions SET expires_at = now() - interval '1 second' WHERE id=$1`, holdID); err != nil {
		t.Fatalf("expire: %v", err)
	}

	// The posted capture legs make the sweeper skip it: n == 0, no new rows.
	if n, err := svc.SweepExpiredHolds(ctx); err != nil || n != 0 {
		t.Fatalf("sweep n=%d err=%v want 0", n, err)
	}
	if got := holdEntryCount(t, svc, holdID); got != 4 {
		t.Fatalf("entries after sweep %d want 4 (captured hold reprocessed!)", got)
	}
	if d := testhelpers.GlobalDrift(t, pool); d != 0 {
		t.Fatalf("global drift %d want 0", d)
	}
}
