package ledger

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/go-ledger/internal/testhelpers"
)

func TestTransfer_HappyPath(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	src, dst := testhelpers.NewFundedPair(t, pool, 100000)
	svc := New(pool)
	beforeTx := testhelpers.TransactionCount(t, pool)
	beforeEntries := testhelpers.LedgerEntryCount(t, pool)

	txID, err := svc.Transfer(context.Background(), src, dst, 1000, "USD")
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if txID == uuid.Nil {
		t.Fatal("nil tx id")
	}
	if got := testhelpers.RawBalance(t, pool, src); got != 99000 {
		t.Fatalf("src balance %d want 99000", got)
	}
	if got := testhelpers.RawBalance(t, pool, dst); got != 1000 {
		t.Fatalf("dst balance %d want 1000", got)
	}
	if drift := testhelpers.GlobalDrift(t, pool); drift != 0 {
		t.Fatalf("global drift %d want 0", drift)
	}
	if n := testhelpers.TransactionCount(t, pool); n != beforeTx+1 {
		t.Fatalf("tx count %d want %d", n, beforeTx+1)
	}
	if n := testhelpers.LedgerEntryCount(t, pool); n != beforeEntries+2 {
		t.Fatalf("entries %d want %d", n, beforeEntries+2)
	}
	var perTxDrift int64
	err = pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(CASE WHEN direction='credit' THEN amount ELSE -amount END),0) FROM ledger_entries WHERE transaction_id=$1`, txID).Scan(&perTxDrift)
	if err != nil {
		t.Fatalf("per-tx drift: %v", err)
	}
	if perTxDrift != 0 {
		t.Fatalf("per-tx drift %d want 0", perTxDrift)
	}
}

func TestTransfer_InsufficientFundsWritesNothing(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	src, dst := testhelpers.NewFundedPair(t, pool, 5000)
	svc := New(pool)
	beforeTx := testhelpers.TransactionCount(t, pool)
	beforeEntries := testhelpers.LedgerEntryCount(t, pool)

	_, err := svc.Transfer(context.Background(), src, dst, 10000, "USD")
	if !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("want ErrInsufficientFunds, got %v", err)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code != "23514" {
		t.Fatalf("unexpected pg code %s", pgErr.Code)
	}
	if n := testhelpers.TransactionCount(t, pool); n != beforeTx {
		t.Fatalf("tx count changed %d -> %d", beforeTx, n)
	}
	if n := testhelpers.LedgerEntryCount(t, pool); n != beforeEntries {
		t.Fatalf("entry count changed %d -> %d", beforeEntries, n)
	}
	if got := testhelpers.RawBalance(t, pool, src); got != 5000 {
		t.Fatalf("src balance %d want 5000", got)
	}
	if drift := testhelpers.GlobalDrift(t, pool); drift != 0 {
		t.Fatalf("drift %d want 0", drift)
	}
}

func TestTransfer_ZeroNegativeRejected(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	src, dst := testhelpers.NewFundedPair(t, pool, 10000)
	svc := New(pool)
	beforeTx := testhelpers.TransactionCount(t, pool)
	beforeEntries := testhelpers.LedgerEntryCount(t, pool)
	for _, amt := range []int64{0, -100, -1} {
		_, err := svc.Transfer(context.Background(), src, dst, amt, "USD")
		if !errors.Is(err, ErrValidation) {
			t.Fatalf("amount %d: want ErrValidation got %v", amt, err)
		}
	}
	if n := testhelpers.TransactionCount(t, pool); n != beforeTx {
		t.Fatalf("tx count changed %d -> %d", beforeTx, n)
	}
	if n := testhelpers.LedgerEntryCount(t, pool); n != beforeEntries {
		t.Fatalf("entry count changed %d -> %d", beforeEntries, n)
	}
}

func TestTransfer_SameAccount(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	src, _ := testhelpers.NewFundedPair(t, pool, 10000)
	svc := New(pool)
	before := testhelpers.TransactionCount(t, pool)
	_, err := svc.Transfer(context.Background(), src, src, 100, "USD")
	if !errors.Is(err, ErrSameAccount) {
		t.Fatalf("want ErrSameAccount got %v", err)
	}
	if n := testhelpers.TransactionCount(t, pool); n != before {
		t.Fatalf("tx count changed")
	}
}

func TestTransfer_NonexistentAccount(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	src, dst := testhelpers.NewFundedPair(t, pool, 10000)
	svc := New(pool)
	fake := uuid.New()
	_, err := svc.Transfer(context.Background(), fake, dst, 100, "USD")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("fake src: want ErrNotFound got %v", err)
	}
	_, err = svc.Transfer(context.Background(), src, fake, 100, "USD")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("fake dst: want ErrNotFound got %v", err)
	}
}

func TestTransfer_CurrencyMismatch(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	testhelpers.TruncateAll(t, pool)
	sysUser, sysAcct := testhelpers.CreateSystemAccount(t, pool, "USD")
	u1 := testhelpers.CreateUser(t, pool, "u1-"+uuid.NewString()+"@test.local")
	u2 := testhelpers.CreateUser(t, pool, "u2-"+uuid.NewString()+"@test.local")
	src := testhelpers.CreateAccount(t, pool, u1, "USD", "available")
	dst := testhelpers.CreateAccount(t, pool, u2, "EUR", "available")
	testhelpers.FundAccount(t, pool, sysAcct, src, 10000, "USD")
	_ = sysUser
	svc := New(pool)
	_, err := svc.Transfer(context.Background(), src, dst, 100, "USD")
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("currency mismatch USD->EUR: want ErrValidation got %v", err)
	}
	_, err = svc.Transfer(context.Background(), src, dst, 100, "EUR")
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("currency mismatch EUR: want ErrValidation got %v", err)
	}
	dst2 := testhelpers.CreateAccount(t, pool, u2, "USD", "available")
	_, err = svc.Transfer(context.Background(), src, dst2, 100, "EUR")
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("currency param EUR vs USD accounts: want ErrValidation got %v", err)
	}
}

func TestTransfer_Concurrency(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	const funded int64 = 100000
	const amount int64 = 1000
	const attempts = 200
	src, dst := testhelpers.NewFundedPair(t, pool, funded)
	svc := New(pool)
	beforeTx := testhelpers.TransactionCount(t, pool)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var observedNegative atomic.Bool
	var readerErr atomic.Value // stores error, checked on main goroutine after wgReader.Wait()
	var wgReader sync.WaitGroup
	wgReader.Add(1)
	go func() {
		defer wgReader.Done()
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				var bal int64
				if err := pool.QueryRow(context.Background(),
					`SELECT COALESCE(SUM(CASE WHEN direction='credit' THEN amount ELSE -amount END),0) FROM ledger_entries WHERE account_id=$1 AND status='posted'`, src).Scan(&bal); err != nil {
					readerErr.Store(err)
				} else if bal < 0 {
					observedNegative.Store(true)
				}
				b, err := svc.GetBalance(context.Background(), src)
				if err != nil {
					readerErr.Store(err)
				} else if b.Available < 0 {
					observedNegative.Store(true)
				}
			}
		}
	}()

	var successCount atomic.Int64
	var insufficientCount atomic.Int64
	var otherErrCount atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Transfer(context.Background(), src, dst, amount, "USD")
			if err == nil {
				successCount.Add(1)
				return
			}
			if errors.Is(err, ErrInsufficientFunds) {
				insufficientCount.Add(1)
				return
			}
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23514" {
				insufficientCount.Add(1)
				return
			}
			t.Logf("unexpected error: %v", err)
			otherErrCount.Add(1)
		}()
	}
	wg.Wait()
	cancel()
	wgReader.Wait()

	if v := readerErr.Load(); v != nil {
		t.Fatalf("background balance reader: %v", v.(error))
	}
	if observedNegative.Load() {
		t.Fatal("observed negative balance during storm")
	}
	if otherErrCount.Load() != 0 {
		t.Fatalf("got %d unexpected errors (would be 500s)", otherErrCount.Load())
	}
	expectedSuccess := funded / amount
	if got := successCount.Load(); got != expectedSuccess {
		t.Fatalf("success count %d want %d (insufficient %d)", got, expectedSuccess, insufficientCount.Load())
	}
	if got := insufficientCount.Load(); got != attempts-expectedSuccess {
		t.Fatalf("insufficient count %d want %d", got, attempts-expectedSuccess)
	}
	if got := testhelpers.RawBalance(t, pool, src); got != funded-successCount.Load()*amount {
		t.Fatalf("final src balance %d want %d", got, funded-successCount.Load()*amount)
	}
	if got := testhelpers.RawBalance(t, pool, dst); got != successCount.Load()*amount {
		t.Fatalf("final dst balance %d want %d", got, successCount.Load()*amount)
	}
	if drift := testhelpers.GlobalDrift(t, pool); drift != 0 {
		t.Fatalf("global drift %d want 0", drift)
	}
	expectedTx := beforeTx + successCount.Load()
	if n := testhelpers.TransactionCount(t, pool); n != expectedTx {
		t.Fatalf("tx count %d want %d", n, expectedTx)
	}
	b, _ := svc.GetBalance(context.Background(), src)
	if b.Available < 0 || b.Posted < 0 {
		t.Fatalf("final balance negative: %+v", b)
	}
}

func TestTransfer_DeadlockProbe(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	testhelpers.TruncateAll(t, pool)
	sysUser, sysAcct := testhelpers.CreateSystemAccount(t, pool, "USD")
	_ = sysUser
	u1 := testhelpers.CreateUser(t, pool, "a-"+uuid.NewString()+"@test.local")
	u2 := testhelpers.CreateUser(t, pool, "b-"+uuid.NewString()+"@test.local")
	accA := testhelpers.CreateAccount(t, pool, u1, "USD", "available")
	accB := testhelpers.CreateAccount(t, pool, u2, "USD", "available")
	testhelpers.FundAccount(t, pool, sysAcct, accA, 100000, "USD")
	testhelpers.FundAccount(t, pool, sysAcct, accB, 100000, "USD")
	svc := New(pool)

	done := make(chan error, 40)
	for i := 0; i < 20; i++ {
		go func() { _, err := svc.Transfer(context.Background(), accA, accB, 100, "USD"); done <- err }()
		go func() { _, err := svc.Transfer(context.Background(), accB, accA, 100, "USD"); done <- err }()
	}
	timeout := time.After(10 * time.Second)
	for i := 0; i < 40; i++ {
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, ErrInsufficientFunds) {
				var pgErr *pgconn.PgError
				if errors.As(err, &pgErr) && pgErr.Code == "23514" {
					continue
				}
				if !errors.Is(err, ErrValidation) && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrSameAccount) {
					t.Fatalf("deadlock probe unexpected err: %v", err)
				}
			}
		case <-timeout:
			t.Fatal("deadlock probe timeout — ORDER BY id FOR UPDATE may be missing")
		}
	}
	if drift := testhelpers.GlobalDrift(t, pool); drift != 0 {
		t.Fatalf("drift %d want 0", drift)
	}
}
