package ledger

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func hashKey(raw string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return hex.EncodeToString(h[:])
}

func generateRawKey() (raw, hash, prefix string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	raw = "sk_live_" + hex.EncodeToString(b)
	hash = hashKey(raw)
	prefix = raw[:12]
	return
}

type ApiKeyInfo struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	Prefix    string    `json:"key_prefix"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	CreatedAt string    `json:"created_at"`
}

func (s *Service) CreateUserWithKey(ctx context.Context, email, role string) (userID uuid.UUID, rawKey string, keyID uuid.UUID, err error) {
	if role == "" {
		role = "user"
	}
	if role != "user" && role != "service" {
		return uuid.Nil, "", uuid.Nil, fmt.Errorf("%w: invalid role", ErrValidation)
	}
	raw, hash, prefix := generateRawKey()
	err = s.pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`, email).Scan(&userID)
	if err != nil {
		return
	}
	err = s.pool.QueryRow(ctx, `INSERT INTO api_keys (user_id, key_hash, key_prefix, role) VALUES ($1,$2,$3,$4) RETURNING id`, userID, hash, prefix, role).Scan(&keyID)
	if err != nil {
		return
	}
	rawKey = raw
	return
}

func (s *Service) CreateApiKey(ctx context.Context, userID uuid.UUID, role string) (raw string, info ApiKeyInfo, err error) {
	if role == "" {
		role = "user"
	}
	raw, hash, prefix := generateRawKey()
	err = s.pool.QueryRow(ctx, `INSERT INTO api_keys (user_id, key_hash, key_prefix, role) VALUES ($1,$2,$3,$4) RETURNING id, user_id, key_prefix, role, status`, userID, hash, prefix, role).Scan(&info.ID, &info.UserID, &info.Prefix, &info.Role, &info.Status)
	if err != nil {
		return
	}
	info.Prefix = prefix
	return raw, info, nil
}

func (s *Service) ListApiKeys(ctx context.Context, userID uuid.UUID) ([]ApiKeyInfo, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, user_id, key_prefix, role, status, created_at::text FROM api_keys WHERE user_id=$1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ApiKeyInfo
	for rows.Next() {
		var a ApiKeyInfo
		var created string
		if err := rows.Scan(&a.ID, &a.UserID, &a.Prefix, &a.Role, &a.Status, &created); err != nil {
			return nil, err
		}
		a.CreatedAt = created
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Service) RevokeApiKey(ctx context.Context, userID, keyID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `UPDATE api_keys SET status='revoked', revoked_at=now() WHERE id=$1 AND user_id=$2 AND status='active'`, keyID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) Authenticate(ctx context.Context, raw string) (userID uuid.UUID, role string, keyID uuid.UUID, prefix string, err error) {
	hash := hashKey(raw)
	var status string
	err = s.pool.QueryRow(ctx, `SELECT user_id, role, id, key_prefix, status FROM api_keys WHERE key_hash=$1`, hash).Scan(&userID, &role, &keyID, &prefix, &status)
	if err != nil {
		if err == pgx.ErrNoRows {
			return uuid.Nil, "", uuid.Nil, "", ErrNotFound
		}
		return
	}
	if status != "active" {
		return uuid.Nil, "", uuid.Nil, "", ErrNotFound
	}
	_, _ = s.pool.Exec(ctx, `UPDATE api_keys SET last_used_at=now() WHERE id=$1 AND (last_used_at IS NULL OR last_used_at < now() - interval '5 minutes')`, keyID)
	return
}
