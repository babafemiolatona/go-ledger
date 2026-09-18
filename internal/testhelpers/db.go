package testhelpers

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://postgres:postgres@localhost:5432/ledger?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		alt := "postgres://ledger:ledger@localhost:5432/ledger?sslmode=disable"
		if url != alt {
			pool.Close()
			pool2, err2 := pgxpool.New(context.Background(), alt)
			if err2 == nil && pool2.Ping(context.Background()) == nil {
				t.Cleanup(func() { pool2.Close() })
				return pool2
			}
			if pool2 != nil {
				pool2.Close()
			}
		}
		t.Fatalf("ping db %q: %v", url, err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

func AcquireAdvisoryLock(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire conn for advisory lock: %v", err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, int64(727727)); err != nil {
		conn.Release()
		t.Fatalf("advisory lock: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, int64(727727))
		conn.Release()
	})
}

func TruncateAll(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `TRUNCATE ledger_entries, idempotency_keys, outbox, transactions, accounts, users CASCADE`)
	if err != nil {
		_, err2 := pool.Exec(context.Background(), `TRUNCATE ledger_entries, idempotency_keys, transactions, accounts, users CASCADE`)
		if err2 != nil {
			t.Fatalf("truncate: %v / %v", err, err2)
		}
	}
}

func CreateUser(t *testing.T, pool *pgxpool.Pool, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(), `INSERT INTO users (email) VALUES ($1) RETURNING id`, email).Scan(&id)
	if err != nil {
		t.Fatalf("create user %q: %v", email, err)
	}
	return id
}

func CreateAccount(t *testing.T, pool *pgxpool.Pool, ownerID uuid.UUID, currency, purpose string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO accounts (owner_id, currency, purpose) VALUES ($1,$2,$3) RETURNING id`, ownerID, currency, purpose).Scan(&id)
	if err != nil {
		t.Fatalf("create account %v/%v/%v: %v", ownerID, currency, purpose, err)
	}
	return id
}

func CreateSystemAccount(t *testing.T, pool *pgxpool.Pool, currency string) (userID, accountID uuid.UUID) {
	t.Helper()
	userID = CreateUser(t, pool, fmt.Sprintf("system-%s-%s@test.local", currency, uuid.NewString()))
	err := pool.QueryRow(context.Background(),
		`INSERT INTO accounts (owner_id, currency, purpose, is_system) VALUES ($1,$2,'available',TRUE) RETURNING id`, userID, currency).Scan(&accountID)
	if err != nil {
		t.Fatalf("create system account: %v", err)
	}
	return
}

func FundAccount(t *testing.T, pool *pgxpool.Pool, systemAccountID, targetAccountID uuid.UUID, amount int64, currency string) {
	t.Helper()
	if amount <= 0 {
		t.Fatalf("fund amount must be >0")
	}
	var txID uuid.UUID
	err := pool.QueryRow(context.Background(), `INSERT INTO transactions (type) VALUES ('deposit') RETURNING id`).Scan(&txID)
	if err != nil {
		t.Fatalf("fund tx insert: %v", err)
	}
	_, err = pool.Exec(context.Background(),
		`INSERT INTO ledger_entries (transaction_id, account_id, direction, amount, currency, status)
		 VALUES ($1,$2,'debit',$3,$4,'posted'), ($1,$5,'credit',$3,$4,'posted')`,
		txID, systemAccountID, amount, currency, targetAccountID)
	if err != nil {
		t.Fatalf("fund entries: %v", err)
	}
}

func NewFundedPair(t *testing.T, pool *pgxpool.Pool, fundedAmount int64) (srcID, dstID uuid.UUID) {
	t.Helper()
	TruncateAll(t, pool)
	sysUser, sysAcct := CreateSystemAccount(t, pool, "USD")
	_ = sysUser
	srcUser := CreateUser(t, pool, "src-"+uuid.NewString()+"@test.local")
	dstUser := CreateUser(t, pool, "dst-"+uuid.NewString()+"@test.local")
	srcID = CreateAccount(t, pool, srcUser, "USD", "available")
	dstID = CreateAccount(t, pool, dstUser, "USD", "available")
	FundAccount(t, pool, sysAcct, srcID, fundedAmount, "USD")
	return
}

func RawBalance(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID) int64 {
	t.Helper()
	var bal int64
	err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(CASE WHEN direction='credit' THEN amount ELSE -amount END),0) FROM ledger_entries WHERE account_id=$1 AND status='posted'`, accountID).Scan(&bal)
	if err != nil {
		t.Fatalf("raw balance: %v", err)
	}
	return bal
}

func GlobalDrift(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var drift int64
	err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(CASE WHEN direction='credit' THEN amount ELSE -amount END),0) FROM ledger_entries WHERE status='posted'`).Scan(&drift)
	if err != nil {
		t.Fatalf("global drift: %v", err)
	}
	return drift
}

func TransactionCount(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var n int64
	err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM transactions`).Scan(&n)
	if err != nil {
		t.Fatalf("tx count: %v", err)
	}
	return n
}

func LedgerEntryCount(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var n int64
	err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM ledger_entries`).Scan(&n)
	if err != nil {
		t.Fatalf("entry count: %v", err)
	}
	return n
}

func InsertUnbalancedEntry(t *testing.T, pool *pgxpool.Pool, a1, a2 uuid.UUID, amt int64, cur string) {
	t.Helper()
	var txID uuid.UUID
	pool.QueryRow(context.Background(), `INSERT INTO transactions (type) VALUES ('transfer') RETURNING id`).Scan(&txID)
	pool.Exec(context.Background(), `INSERT INTO ledger_entries (transaction_id, account_id, direction, amount, currency, status) VALUES ($1,$2,'debit',$3,$4,'posted')`, txID, a1, amt, cur)
}
