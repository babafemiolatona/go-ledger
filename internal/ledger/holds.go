package ledger

import (
	"context"
	"fmt"
	"strings"
	"time"

	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *Service) Hold(ctx context.Context, srcID, dstID uuid.UUID, amount int64, currency string, expiresAt time.Time) (uuid.UUID, error) {
	if amount <= 0 {
		return uuid.Nil, fmt.Errorf("%w: amount must be >0", ErrValidation)
	}
	if srcID == dstID {
		return uuid.Nil, ErrSameAccount
	}
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if len(currency) != 3 {
		return uuid.Nil, fmt.Errorf("%w: currency required", ErrValidation)
	}
	if expiresAt.IsZero() || expiresAt.Before(time.Now()) {
		return uuid.Nil, fmt.Errorf("%w: expires_at required and future", ErrValidation)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `SELECT id, owner_id, currency, status, purpose, is_system FROM accounts WHERE id IN ($1,$2) ORDER BY id FOR UPDATE`, srcID, dstID)
	if err != nil {
		return uuid.Nil, err
	}
	type r struct {
		id, owner                 uuid.UUID
		currency, status, purpose string
		isSystem                  bool
	}
	m := map[uuid.UUID]r{}
	for rows.Next() {
		var x r
		if err := rows.Scan(&x.id, &x.owner, &x.currency, &x.status, &x.purpose, &x.isSystem); err != nil {
			rows.Close()
			return uuid.Nil, err
		}
		m[x.id] = x
	}
	rows.Close()
	src, ok := m[srcID]
	if !ok {
		return uuid.Nil, fmt.Errorf("%w: source account", ErrNotFound)
	}
	dst, ok := m[dstID]
	if !ok {
		return uuid.Nil, fmt.Errorf("%w: destination account", ErrNotFound)
	}
	if src.status != "active" || dst.status != "active" {
		return uuid.Nil, fmt.Errorf("%w: account not active", ErrValidation)
	}
	if src.currency != currency || dst.currency != currency {
		return uuid.Nil, fmt.Errorf("%w: currency mismatch", ErrValidation)
	}
	if src.purpose != "available" || dst.purpose != "escrow" {
		return uuid.Nil, fmt.Errorf("%w: hold requires available->escrow", ErrValidation)
	}
	var posted, pending int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(CASE WHEN direction='credit' THEN amount ELSE -amount END),0) FROM ledger_entries WHERE account_id=$1 AND status='posted'`, srcID).Scan(&posted); err != nil {
		return uuid.Nil, err
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(CASE WHEN direction='credit' THEN amount ELSE -amount END),0) FROM ledger_entries WHERE account_id=$1 AND status='pending'`, srcID).Scan(&pending); err != nil {
		return uuid.Nil, err
	}
	available := posted + pending
	if !src.isSystem && available < amount {
		return uuid.Nil, ErrInsufficientFunds
	}

	var holdID uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO transactions (type, expires_at) VALUES ('hold',$1) RETURNING id`, expiresAt).Scan(&holdID); err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO ledger_entries (transaction_id, account_id, direction, amount, currency, status) VALUES ($1,$2,'debit',$4,$5,'pending'), ($1,$3,'credit',$4,$5,'pending')`, holdID, srcID, dstID, amount, currency); err != nil {
		return uuid.Nil, err
	}
	if err := insertOutbox(ctx, tx, "transaction", holdID, "ledger.hold.created", map[string]any{"hold_id": holdID, "from": srcID, "to": dstID, "amount": amount, "currency": currency}); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23514" {
			return uuid.Nil, fmt.Errorf("%w: %w", ErrInsufficientFunds, err)
		}
		return uuid.Nil, err
	}
	return holdID, nil
}

func (s *Service) GetHoldSourceOwner(ctx context.Context, holdID uuid.UUID) (srcID, ownerID uuid.UUID, err error) {
	if err := s.pool.QueryRow(ctx, `SELECT account_id FROM ledger_entries WHERE transaction_id=$1 AND direction='debit' LIMIT 1`, holdID).Scan(&srcID); err != nil {
		if err == pgx.ErrNoRows {
			return uuid.Nil, uuid.Nil, ErrNotFound
		}
		return uuid.Nil, uuid.Nil, err
	}
	ownerID, err = s.GetAccountOwner(ctx, srcID)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return srcID, ownerID, nil
}

func (s *Service) Capture(ctx context.Context, holdID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var txType string
	var expires *time.Time
	if err := tx.QueryRow(ctx, `SELECT type, expires_at FROM transactions WHERE id=$1`, holdID).Scan(&txType, &expires); err != nil {
		if err == pgx.ErrNoRows {
			return ErrNotFound
		}
		return err
	}
	if txType != "hold" {
		return fmt.Errorf("%w: not a hold", ErrValidation)
	}
	if expires != nil && expires.Before(time.Now()) {
		return fmt.Errorf("%w: hold expired", ErrValidation)
	}
	rows, err := tx.Query(ctx, `SELECT account_id, direction, amount, currency, status FROM ledger_entries WHERE transaction_id=$1 ORDER BY id`, holdID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var srcID, dstID uuid.UUID
	var amount int64
	var currency string
	var hasPosted bool
	cnt := 0
	for rows.Next() {
		var aid uuid.UUID
		var dir string
		var amt int64
		var cur, st string
		if err := rows.Scan(&aid, &dir, &amt, &cur, &st); err != nil {
			return err
		}
		if st == "posted" {
			hasPosted = true
		}
		if cnt == 0 {
			amount = amt
			currency = cur
			if dir == "debit" {
				srcID = aid
			} else {
				return fmt.Errorf("%w: invalid hold legs", ErrValidation)
			}
		}
		if cnt == 1 {
			if dir != "credit" {
				return fmt.Errorf("%w: invalid hold legs", ErrValidation)
			}
			dstID = aid
			if amt != amount || cur != currency {
				return fmt.Errorf("%w: mismatched hold legs", ErrValidation)
			}
		}
		cnt++
	}
	if cnt != 2 {
		return fmt.Errorf("%w: hold must have 2 pending legs", ErrValidation)
	}
	if hasPosted {
		return fmt.Errorf("%w: already captured/released", ErrValidation)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO ledger_entries (transaction_id, account_id, direction, amount, currency, status) VALUES ($1,$2,'debit',$4,$5,'posted'), ($1,$3,'credit',$4,$5,'posted')`, holdID, srcID, dstID, amount, currency); err != nil {
		return err
	}
	if err := insertOutbox(ctx, tx, "transaction", holdID, "ledger.hold.captured", map[string]any{"hold_id": holdID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) Release(ctx context.Context, holdID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var txType string
	if err := tx.QueryRow(ctx, `SELECT type FROM transactions WHERE id=$1`, holdID).Scan(&txType); err != nil {
		if err == pgx.ErrNoRows {
			return ErrNotFound
		}
		return err
	}
	if txType != "hold" {
		return fmt.Errorf("%w: not a hold", ErrValidation)
	}
	rows, err := tx.Query(ctx, `SELECT account_id, direction, amount, currency, status FROM ledger_entries WHERE transaction_id=$1 ORDER BY id`, holdID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var srcID, dstID uuid.UUID
	var amount int64
	var currency string
	var hasPosted bool
	cnt := 0
	for rows.Next() {
		var aid uuid.UUID
		var dir string
		var amt int64
		var cur, st string
		if err := rows.Scan(&aid, &dir, &amt, &cur, &st); err != nil {
			return err
		}
		if st == "posted" {
			hasPosted = true
		}
		if cnt == 0 {
			amount = amt
			currency = cur
			srcID = aid
		}
		if cnt == 1 {
			dstID = aid
		}
		cnt++
	}
	if cnt != 2 {
		return fmt.Errorf("%w: hold must have 2 pending legs", ErrValidation)
	}
	if hasPosted {
		return fmt.Errorf("%w: already captured/released", ErrValidation)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO ledger_entries (transaction_id, account_id, direction, amount, currency, status) VALUES ($1,$2,'credit',$4,$5,'pending'), ($1,$3,'debit',$4,$5,'pending')`, holdID, srcID, dstID, amount, currency); err != nil {
		return err
	}
	if err := insertOutbox(ctx, tx, "transaction", holdID, "ledger.hold.released", map[string]any{"hold_id": holdID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
