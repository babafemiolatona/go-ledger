package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/go-ledger/internal/config"
	"github.com/go-ledger/internal/db"
	"github.com/go-ledger/internal/http/handlers"
	apmw "github.com/go-ledger/internal/http/middleware"
	"github.com/go-ledger/internal/ledger"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		return fmt.Errorf("invalid LOG_LEVEL: %w", err)
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	ctx := context.Background()
	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	svc := ledger.New(pool)

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(apmw.RequestLogger(log))
	r.Use(middleware.Recoverer)

	r.Get("/healthz", handlers.Healthz)
	r.Get("/readyz", handlers.Readyz(pool))
	r.Get("/docs", handlers.Docs)
	r.Get("/docs/openapi.yaml", handlers.OpenAPISpec)

	r.Post("/v1/users", handlers.CreateUser(svc))

	r.Route("/v1", func(r chi.Router) {
		r.Use(apmw.Auth(svc))
		r.Post("/accounts", handlers.CreateAccount(svc))
		r.Get("/accounts", handlers.ListAccounts(svc))
		r.Get("/accounts/{id}/statement", handlers.GetStatement(svc))
		r.Get("/transactions/{id}", handlers.GetTransaction(svc))
		r.Get("/accounts/{id}/balance", handlers.GetBalance(svc))
		r.Post("/transfers", handlers.Transfer(svc))
		r.Get("/api-keys", handlers.ListApiKeys(svc))
		r.Post("/api-keys", handlers.CreateApiKey(svc))
		r.Delete("/api-keys/{id}", handlers.RevokeApiKey(svc))
		r.Post("/holds", handlers.CreateHold(svc))
		r.Post("/holds/{id}/capture", handlers.CaptureHold(svc))
		r.Post("/holds/{id}/release", handlers.ReleaseHold(svc))
		r.Post("/webhooks", handlers.CreateWebhook(svc))
		r.Get("/webhooks", handlers.ListWebhooks(svc))
		r.Get("/webhooks/deliveries", handlers.ListWebhookDeliveries(svc))
	})

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	stopCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-stopCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Info("listening", "port", cfg.Port)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}
