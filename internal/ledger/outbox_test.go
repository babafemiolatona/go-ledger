package ledger

import (
	"context"
	"testing"

	"github.com/go-ledger/internal/testhelpers"
)

func TestOutbox_AtomicWithTransfer(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	testhelpers.TruncateAll(t, pool)
	src, dst := testhelpers.NewFundedPair(t, pool, 100000)
	svc := New(pool)
	_, err := svc.Transfer(context.Background(), src, dst, 1000, "USD")
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox WHERE published_at IS NULL AND topic='ledger.transfer.posted'`).Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 1 {
		t.Fatalf("outbox unpublished %d want 1", n)
	}
}

func TestOutbox_WorkerPublishes(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	testhelpers.TruncateAll(t, pool)
	src, dst := testhelpers.NewFundedPair(t, pool, 100000)
	svc := New(pool)
	svc.Transfer(context.Background(), src, dst, 500, "USD")
	var id int64
	err := pool.QueryRow(context.Background(), `SELECT id FROM outbox WHERE published_at IS NULL ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&id)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE outbox SET published_at=now() WHERE id=$1`, id); err != nil {
		t.Fatalf("ack: %v", err)
	}
	var published int
	pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox WHERE published_at IS NOT NULL`).Scan(&published)
	if published != 1 {
		t.Fatalf("published %d", published)
	}
}

func TestOutbox_CrashBetweenCommitAndPublishStillDeliversOnRestart(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	testhelpers.TruncateAll(t, pool)
	src, dst := testhelpers.NewFundedPair(t, pool, 100000)
	svc := New(pool)
	txID, _ := svc.Transfer(context.Background(), src, dst, 1000, "USD")
	var outboxID int64
	if err := pool.QueryRow(context.Background(), `SELECT id FROM outbox WHERE aggregate_id=$1 AND published_at IS NULL`, txID).Scan(&outboxID); err != nil {
		t.Fatalf("outbox missing: %v", err)
	}
	if outboxID == 0 {
		t.Fatalf("outbox missing")
	}
	var id2 int64
	if err := pool.QueryRow(context.Background(), `SELECT id FROM outbox WHERE published_at IS NULL LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&id2); err != nil {
		t.Fatalf("restart poll: %v", err)
	}
	if id2 != outboxID {
		t.Fatalf("restart should find same %d vs %d", outboxID, id2)
	}
}
