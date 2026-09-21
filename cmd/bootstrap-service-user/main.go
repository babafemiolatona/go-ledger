package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/go-ledger/internal/config"
	"github.com/go-ledger/internal/db"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: bootstrap-service-user <email>")
		os.Exit(1)
	}
	email := strings.TrimSpace(os.Args[1])
	if email == "" {
		fmt.Fprintln(os.Stderr, "email required")
		os.Exit(1)
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "load config:", err)
		os.Exit(1)
	}
	pool, err := db.NewPool(context.Background(), cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "db pool:", err)
		os.Exit(1)
	}
	defer pool.Close()

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		fmt.Fprintln(os.Stderr, "rand:", err)
		os.Exit(1)
	}
	raw := "sk_live_" + hex.EncodeToString(b)
	h := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	hash := hex.EncodeToString(h[:])
	prefix := raw[:12]

	var userID, keyID string
	err = pool.QueryRow(context.Background(), `INSERT INTO users (email) VALUES ($1) RETURNING id`, email).Scan(&userID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "insert user:", err)
		os.Exit(1)
	}
	err = pool.QueryRow(context.Background(), `INSERT INTO api_keys (user_id, key_hash, key_prefix, role) VALUES ($1,$2,$3,'service') RETURNING id`, userID, hash, prefix).Scan(&keyID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "insert api_key:", err)
		os.Exit(1)
	}
	fmt.Printf("user_id=%s key_id=%s api_key=%s role=service prefix=%s\n", userID, keyID, raw, prefix)
	fmt.Fprintln(os.Stderr, "Store api_key securely — it will not be shown again.")
}
