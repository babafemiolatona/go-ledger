package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/go-ledger/internal/config"
	"github.com/go-ledger/internal/db"
	"github.com/go-ledger/internal/ledger"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load()
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}
	pool, err := db.NewPool(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Error("db pool", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	ctx := context.Background()
	svc := ledger.New(pool)
	res, err := svc.Reconcile(ctx)
	if err != nil {
		log.Error("reconcile", "err", err)
		os.Exit(1)
	}
	fmt.Printf("global drift %d perTx %d accounts %d status %s\n", res.GlobalDrift, len(res.PerTxDrift), len(res.PerAcctDrift), res.Status)
	if _, err := pool.Exec(ctx, `INSERT INTO reconciliation_runs (finished_at, drift, status) VALUES (now(), $1, $2)`, res.GlobalDrift, res.Status); err != nil {
		log.Warn("record reconciliation run", "err", err)
	}
	if res.Status != "ok" {
		os.Exit(2)
	}
}
