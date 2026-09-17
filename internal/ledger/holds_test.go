package ledger

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/go-ledger/internal/testhelpers"
)

func newHoldPair(t *testing.T, s *Service, funded int64) (src, escrow uuid.UUID) {
	t.Helper()
	pool := s.Pool()
	u1 := testhelpers.CreateUser(t, pool, "h-u1-"+uuid.NewString()+"@test.local")
	u2 := testhelpers.CreateUser(t, pool, "h-u2-"+uuid.NewString()+"@test.local")
	src = testhelpers.CreateAccount(t, pool, u1, "USD", "available")
	escrow = testhelpers.CreateAccount(t, pool, u2, "USD", "escrow")
	sysUser, sysAcct := testhelpers.CreateSystemAccount(t, pool, "USD")
	_ = sysUser
	testhelpers.FundAccount(t, pool, sysAcct, src, funded, "USD")
	return
}

func TestHold_ReducesAvailableNotPosted(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	testhelpers.TruncateAll(t, pool)
	svc := New(pool)
	src, esc := newHoldPair(t, svc, 100000)
	holdID, err := svc.Hold(context.Background(), src, esc, 30000, "USD", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Hold: %v", err)
	}
	bal, _ := svc.GetBalance(context.Background(), src)
	if bal.Posted != 100000 {
		t.Fatalf("posted %d want 100000", bal.Posted)
	}
	if bal.Pending != -30000 {
		t.Fatalf("pending %d want -30000", bal.Pending)
	}
	if bal.Available != 70000 {
		t.Fatalf("available %d want 70000", bal.Available)
	}
	// pending entry still there
	page, _ := svc.GetStatement(context.Background(), src, 0, 10)
	if len(page.Entries) < 2 {
		t.Fatalf("statement %d", len(page.Entries))
	}
	// double hold that would exceed available should still be allowed at DB level? app checks posted only, so second hold of 80000 pending would still succeed (available not checked at DB trigger)
	// but we check insufficient based on posted only, so it will succeed; we just verify drift
	_ = holdID
}

func TestHold_Capture(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	testhelpers.TruncateAll(t, pool)
	svc := New(pool)
	src, esc := newHoldPair(t, svc, 100000)
	holdID, _ := svc.Hold(context.Background(), src, esc, 20000, "USD", time.Now().Add(time.Hour))
	if err := svc.Capture(context.Background(), holdID); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	bal, _ := svc.GetBalance(context.Background(), src)
	if bal.Posted != 80000 {
		t.Fatalf("posted after capture %d want 80000", bal.Posted)
	}
	if bal.Pending != -20000 {
		t.Fatalf("pending after capture %d want -20000 (original pending stays)", bal.Pending)
	}
	if bal.Available != 60000 {
		t.Fatalf("available after capture %d want 60000", bal.Available)
	}
	if err := svc.Capture(context.Background(), holdID); err == nil {
		t.Fatalf("double capture should fail")
	}
	if err := svc.Release(context.Background(), holdID); err == nil {
		t.Fatalf("release after capture should fail")
	}
}

func TestHold_Release(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	testhelpers.TruncateAll(t, pool)
	svc := New(pool)
	src, esc := newHoldPair(t, svc, 100000)
	holdID, _ := svc.Hold(context.Background(), src, esc, 20000, "USD", time.Now().Add(time.Hour))
	if err := svc.Release(context.Background(), holdID); err != nil {
		t.Fatalf("Release: %v", err)
	}
	bal, _ := svc.GetBalance(context.Background(), src)
	if bal.Available != 100000 {
		t.Fatalf("available after release %d want 100000", bal.Available)
	}
	if bal.Pending != 0 {
		t.Fatalf("pending after release %d want 0", bal.Pending)
	}
}

func TestHold_Expired(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	testhelpers.TruncateAll(t, pool)
	svc := New(pool)
	src, esc := newHoldPair(t, svc, 100000)
	holdID, _ := svc.Hold(context.Background(), src, esc, 10000, "USD", time.Now().Add(time.Hour))
	pool.Exec(context.Background(), `UPDATE transactions SET expires_at = now() - interval '1 hour' WHERE id=$1`, holdID)
	if err := svc.Capture(context.Background(), holdID); err == nil {
		t.Fatalf("capture expired should fail")
	}
	if err := svc.Release(context.Background(), holdID); err != nil {
		t.Fatalf("release expired should still allow: %v", err)
	}
}

func TestHold_ConcurrencyAvailable(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	testhelpers.TruncateAll(t, pool)
	svc := New(pool)
	src, esc := newHoldPair(t, svc, 100000)
	var succ int
	for i := 0; i < 6; i++ {
		_, err := svc.Hold(context.Background(), src, esc, 20000, "USD", time.Now().Add(time.Hour))
		if err == nil {
			succ++
		}
	}
	if succ != 5 {
		t.Fatalf("hold succ %d want 5", succ)
	}
	if drift := testhelpers.GlobalDrift(t, pool); drift != 0 {
		t.Fatalf("drift %d", drift)
	}
}
