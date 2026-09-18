package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/go-ledger/internal/config"
	"github.com/go-ledger/internal/db"
	"github.com/go-ledger/internal/ledger"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}
	pool, err := db.NewPool(context.Background(), cfg.DatabaseURL)
	if err != nil {
		slog.Error("db pool", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	svc := ledger.New(pool)

	go func() {
		for {
			time.Sleep(30 * time.Second)
			n, err := svc.SweepExpiredHolds(context.Background())
			if err != nil {
				log.Error("sweep", "err", err)
				continue
			}
			if n > 0 {
				log.Info("swept holds", "n", n)
			}
		}
	}()

	log.Info("worker started", "poll_ms", 1000)
	for {
		rows, err := pool.Query(context.Background(), `SELECT id, topic, payload FROM outbox WHERE published_at IS NULL ORDER BY id LIMIT 10 FOR UPDATE SKIP LOCKED`)
		if err != nil {
			log.Error("outbox query", "err", err)
			time.Sleep(time.Second)
			continue
		}
		var ids []int64
		for rows.Next() {
			var id int64
			var topic string
			var payload []byte
			if err := rows.Scan(&id, &topic, &payload); err != nil {
				continue
			}
			log.Info("publish", "id", id, "topic", topic, "payload", string(payload))
			ids = append(ids, id)
		}
		rows.Close()
		if len(ids) == 0 {
			time.Sleep(time.Second)
			continue
		}
		for _, id := range ids {
			_, err := pool.Exec(context.Background(), `UPDATE outbox SET published_at=now(), attempts=attempts+1 WHERE id=$1`, id)
			if err != nil {
				log.Error("outbox ack", "id", id, "err", err)
			}
		}
	}
}
