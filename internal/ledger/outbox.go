package ledger

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func insertOutbox(ctx context.Context, tx pgx.Tx, aggType string, aggID uuid.UUID, topic string, payload any) error {
	b, _ := json.Marshal(payload)
	_, err := tx.Exec(ctx, `INSERT INTO outbox (aggregate_type, aggregate_id, topic, payload) VALUES ($1,$2,$3,$4)`, aggType, aggID, topic, string(b))
	return err
}
