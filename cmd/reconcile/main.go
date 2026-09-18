package main

import (
	"context"
	"fmt"
	"os"

	"github.com/go-ledger/internal/config"
	"github.com/go-ledger/internal/db"
	"github.com/go-ledger/internal/ledger"
)

func main() {
	cfg, _ := config.Load()
	pool, _ := db.NewPool(context.Background(), cfg.DatabaseURL)
	defer pool.Close()
	svc := ledger.New(pool)
	res, _ := svc.Reconcile(context.Background())
	fmt.Printf("global drift %d perTx %d accounts %d status %s\n", res.GlobalDrift, len(res.PerTxDrift), len(res.PerAcctDrift), res.Status)
	pool.Exec(context.Background(), `INSERT INTO reconciliation_runs (finished_at, drift, status) VALUES (now(), $1, $2)`, res.GlobalDrift, res.Status)
	if res.Status != "ok" {
		os.Exit(2)
	}
}
