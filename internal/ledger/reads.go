package ledger

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type StatementEntry struct {
	ID                    int64      `json:"id"`
	TransactionID         uuid.UUID  `json:"transaction_id"`
	TransactionType       string     `json:"transaction_type"`
	AccountID             uuid.UUID  `json:"account_id"`
	Direction             string     `json:"direction"`
	Amount                int64      `json:"amount"`
	Currency              string     `json:"currency"`
	Status                string     `json:"status"`
	CreatedAt             time.Time  `json:"created_at"`
	RunningBalance        int64      `json:"running_balance"`
	CounterpartyAccountID *uuid.UUID `json:"counterparty_account_id,omitempty"`
}

type StatementPage struct {
	Entries    []StatementEntry `json:"entries"`
	NextCursor *int64           `json:"next_cursor,omitempty"`
}

var twoLegTypes = map[string]bool{
	"transfer": true, "payment": true, "withdrawal": true, "deposit": true, "hold": true,
}

func (s *Service) ListAccounts(ctx context.Context, ownerID uuid.UUID) ([]Account, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, owner_id, currency, purpose, status, is_system FROM accounts WHERE owner_id=$1 ORDER BY created_at ASC, id ASC`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.ID, &a.OwnerID, &a.Currency, &a.Purpose, &a.Status, &a.IsSystem); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Service) GetStatement(ctx context.Context, accountID uuid.UUID, cursor int64, limit int) (StatementPage, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE id=$1)`, accountID).Scan(&exists); err != nil {
		return StatementPage{}, err
	}
	if !exists {
		return StatementPage{}, ErrNotFound
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, transaction_id, direction, amount, currency, status, created_at, running_balance, tx_type FROM (
		  SELECT id, transaction_id, direction, amount, currency, status, created_at,
		         SUM(CASE WHEN direction='credit' THEN amount ELSE -amount END) OVER (ORDER BY id) AS running_balance,
		         (SELECT type FROM transactions WHERE id=transaction_id) AS tx_type
		  FROM ledger_entries
		  WHERE account_id=$1
		) sub
		WHERE id > $2
		ORDER BY id ASC
		LIMIT $3`, accountID, cursor, limit+1)
	if err != nil {
		return StatementPage{}, err
	}
	defer rows.Close()

	var entries []StatementEntry
	for rows.Next() {
		var e StatementEntry
		e.AccountID = accountID
		if err := rows.Scan(&e.ID, &e.TransactionID, &e.Direction, &e.Amount, &e.Currency, &e.Status, &e.CreatedAt, &e.RunningBalance, &e.TransactionType); err != nil {
			return StatementPage{}, err
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return StatementPage{}, err
	}

	if len(entries) > 0 {
		txIDs := make([]uuid.UUID, 0, len(entries))
		for _, e := range entries {
			if twoLegTypes[e.TransactionType] {
				txIDs = append(txIDs, e.TransactionID)
			}
		}
		if len(txIDs) > 0 {
			countByTx := map[uuid.UUID]int{}
			rows2, err := s.pool.Query(ctx, `SELECT transaction_id, COUNT(*) FROM ledger_entries WHERE transaction_id = ANY($1) GROUP BY transaction_id`, txIDs)
			if err == nil {
				for rows2.Next() {
					var tid uuid.UUID
					var c int
					_ = rows2.Scan(&tid, &c)
					countByTx[tid] = c
				}
				rows2.Close()
			}
			for i := range entries {
				if !twoLegTypes[entries[i].TransactionType] || countByTx[entries[i].TransactionID] != 2 {
					continue
				}
				var cp uuid.UUID
				err := s.pool.QueryRow(ctx,
					`SELECT account_id FROM ledger_entries WHERE transaction_id=$1 AND account_id != $2 LIMIT 1`,
					entries[i].TransactionID, accountID).Scan(&cp)
				if err == nil {
					entries[i].CounterpartyAccountID = &cp
				} else if err != pgx.ErrNoRows {
					return StatementPage{}, err
				}
			}
		}
	}

	var next *int64
	if len(entries) > limit {
		entries = entries[:limit]
		last := entries[len(entries)-1].ID
		next = &last
	}
	return StatementPage{Entries: entries, NextCursor: next}, nil
}

type TransactionDetail struct {
	ID        uuid.UUID        `json:"id"`
	Type      string           `json:"type"`
	CreatedAt time.Time        `json:"created_at"`
	Entries   []StatementEntry `json:"entries"`
}

func (s *Service) GetTransaction(ctx context.Context, txID uuid.UUID) (TransactionDetail, error) {
	var d TransactionDetail
	var expiresAt *time.Time
	_ = expiresAt
	err := s.pool.QueryRow(ctx, `SELECT id, type, created_at FROM transactions WHERE id=$1`, txID).Scan(&d.ID, &d.Type, &d.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return TransactionDetail{}, ErrNotFound
		}
		return TransactionDetail{}, err
	}

	d.Type = strings.TrimSpace(d.Type)
	rows, err := s.pool.Query(ctx,
		`SELECT id, account_id, direction, amount, currency, status, created_at
		 FROM ledger_entries WHERE transaction_id=$1 ORDER BY id ASC`, txID)
	if err != nil {
		return TransactionDetail{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var e StatementEntry
		e.TransactionID = txID
		e.TransactionType = d.Type
		if err := rows.Scan(&e.ID, &e.AccountID, &e.Direction, &e.Amount, &e.Currency, &e.Status, &e.CreatedAt); err != nil {
			return TransactionDetail{}, err
		}
		d.Entries = append(d.Entries, e)
	}
	if err := rows.Err(); err != nil {
		return TransactionDetail{}, err
	}
	if len(d.Entries) == 0 {
		return TransactionDetail{}, fmt.Errorf("%w: transaction has no entries", ErrNotFound)
	}
	return d, nil
}
