package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const ScopeTransfer = "transfer"

type Service struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (s *Service) Pool() *pgxpool.Pool { return s.pool }

func (s *Service) GetAccountOwner(ctx context.Context, accountID uuid.UUID) (uuid.UUID, error) {
	var ownerID uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT owner_id FROM accounts WHERE id=$1`, accountID).Scan(&ownerID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return uuid.Nil, ErrNotFound
		}
		return uuid.Nil, err
	}
	return ownerID, nil
}

func (s *Service) IsTransactionOwner(ctx context.Context, txID uuid.UUID, callerID uuid.UUID) (bool, error) {
	var cnt int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM ledger_entries le JOIN accounts a ON a.id=le.account_id
		 WHERE le.transaction_id=$1 AND a.owner_id=$2`, txID, callerID).Scan(&cnt)
	if err != nil {
		return false, err
	}
	return cnt > 0, nil
}

type Account struct {
	ID       uuid.UUID `json:"id"`
	OwnerID  uuid.UUID `json:"owner_id"`
	Currency string    `json:"currency"`
	Purpose  string    `json:"purpose"`
	Status   string    `json:"status"`
	IsSystem bool      `json:"is_system"`
}

type Balance struct {
	AccountID uuid.UUID `json:"account_id"`
	Currency  string    `json:"currency"`
	Available int64     `json:"available"` // minor units
	Posted    int64     `json:"posted"`
	Pending   int64     `json:"pending"`
}

func (s *Service) CreateAccount(ctx context.Context, ownerID uuid.UUID, currency, purpose string) (Account, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	purpose = strings.TrimSpace(purpose)
	if currency == "" || len(currency) != 3 {
		return Account{}, fmt.Errorf("%w: currency must be 3 letters", ErrValidation)
	}
	if purpose != "available" && purpose != "escrow" && purpose != "fees" {
		return Account{}, fmt.Errorf("%w: invalid purpose", ErrValidation)
	}
	var a Account
	err := s.pool.QueryRow(ctx,
		`INSERT INTO accounts (owner_id, currency, purpose) VALUES ($1,$2,$3)
		 RETURNING id, owner_id, currency, purpose, status, is_system`,
		ownerID, currency, purpose,
	).Scan(&a.ID, &a.OwnerID, &a.Currency, &a.Purpose, &a.Status, &a.IsSystem)
	if err != nil {
		return Account{}, fmt.Errorf("create account: %w", err)
	}
	return a, nil
}

func (s *Service) GetBalance(ctx context.Context, accountID uuid.UUID) (Balance, error) {
	var b Balance
	var currency string
	err := s.pool.QueryRow(ctx, `SELECT currency FROM accounts WHERE id=$1`, accountID).Scan(&currency)
	if err != nil {
		if err == pgx.ErrNoRows {
			return Balance{}, ErrNotFound
		}
		return Balance{}, err
	}

	var posted, pending int64
	err = s.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(CASE WHEN direction='credit' THEN amount ELSE -amount END),0)
		 FROM ledger_entries WHERE account_id=$1 AND status='posted'`, accountID).Scan(&posted)
	if err != nil {
		return Balance{}, err
	}
	err = s.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(CASE WHEN direction='credit' THEN amount ELSE -amount END),0)
		 FROM ledger_entries WHERE account_id=$1 AND status='pending'`, accountID).Scan(&pending)
	if err != nil {
		return Balance{}, err
	}
	b.AccountID = accountID
	b.Currency = currency
	b.Posted = posted
	b.Pending = pending
	b.Available = posted + pending
	return b, nil
}

func (s *Service) Transfer(ctx context.Context, fromID, toID uuid.UUID, amountMinor int64, currency string) (uuid.UUID, error) {
	if amountMinor <= 0 {
		return uuid.Nil, fmt.Errorf("%w: amount must be > 0", ErrValidation)
	}
	if fromID == toID {
		return uuid.Nil, ErrSameAccount
	}
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if len(currency) != 3 {
		return uuid.Nil, fmt.Errorf("%w: currency required", ErrValidation)
	}

	if amountMinor <= 0 {
		return uuid.Nil, fmt.Errorf("%w: amount must be > 0", ErrValidation)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx,
		`SELECT id, owner_id, currency, status, is_system FROM accounts WHERE id IN ($1,$2) ORDER BY id FOR UPDATE`,
		fromID, toID)
	if err != nil {
		return uuid.Nil, err
	}
	type acctRow struct {
		id       uuid.UUID
		ownerID  uuid.UUID
		currency string
		status   string
		isSystem bool
	}
	found := map[uuid.UUID]acctRow{}
	for rows.Next() {
		var r acctRow
		if err := rows.Scan(&r.id, &r.ownerID, &r.currency, &r.status, &r.isSystem); err != nil {
			rows.Close()
			return uuid.Nil, err
		}
		found[r.id] = r
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return uuid.Nil, err
	}
	src, ok := found[fromID]
	if !ok {
		return uuid.Nil, fmt.Errorf("%w: source account", ErrNotFound)
	}
	dst, ok := found[toID]
	if !ok {
		return uuid.Nil, fmt.Errorf("%w: destination account", ErrNotFound)
	}
	if src.status != "active" || dst.status != "active" {
		return uuid.Nil, fmt.Errorf("%w: account not active", ErrValidation)
	}
	if src.currency != currency || dst.currency != currency {
		return uuid.Nil, fmt.Errorf("%w: currency mismatch", ErrValidation)
	}

	var srcBalance int64
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(CASE WHEN direction='credit' THEN amount ELSE -amount END),0)
		 FROM ledger_entries WHERE account_id=$1 AND status='posted'`, fromID).Scan(&srcBalance)
	if err != nil {
		return uuid.Nil, err
	}

	if !src.isSystem && srcBalance < amountMinor {
		return uuid.Nil, ErrInsufficientFunds
	}

	var txID uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO transactions (type) VALUES ('transfer') RETURNING id`).Scan(&txID)
	if err != nil {
		return uuid.Nil, err
	}

	if amountMinor <= 0 {
		return uuid.Nil, fmt.Errorf("%w: amount must be > 0", ErrValidation)
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO ledger_entries (transaction_id, account_id, direction, amount, currency, status)
		 VALUES ($1,$2,'debit',$4,$5,'posted'), ($1,$3,'credit',$4,$5,'posted')`,
		txID, fromID, toID, amountMinor, currency)
	if err != nil {
		return uuid.Nil, err
	}
	_ = insertOutbox(ctx, tx, "transaction", txID, "ledger.transfer.posted", map[string]any{"transaction_id": txID, "from": fromID, "to": toID, "amount": amountMinor, "currency": currency})

	if err := tx.Commit(ctx); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23514" {
			return uuid.Nil, fmt.Errorf("%w: %w", ErrInsufficientFunds, err)
		}
		return uuid.Nil, err
	}
	return txID, nil
}

func (s *Service) TransferIdempotent(ctx context.Context, scope, key string, fromID, toID uuid.UUID, amountMinor int64, currency string) (uuid.UUID, bool, error) {
	txID, replayed, _, _, err := s.doTransferIdempotent(ctx, scope, key, fromID, toID, amountMinor, currency)
	return txID, replayed, err
}

func (s *Service) TransferIdempotentWithResponse(ctx context.Context, scope, key string, fromID, toID uuid.UUID, amountMinor int64, currency string) (txID uuid.UUID, replayed bool, code int, body []byte, err error) {
	return s.doTransferIdempotent(ctx, scope, key, fromID, toID, amountMinor, currency)
}

func (s *Service) doTransferIdempotent(ctx context.Context, scope, key string, fromID, toID uuid.UUID, amountMinor int64, currency string) (txID uuid.UUID, replayed bool, code int, body []byte, err error) {
	if strings.TrimSpace(key) == "" {
		txID, err = s.Transfer(ctx, fromID, toID, amountMinor, currency)
		if err != nil {
			return uuid.Nil, false, 0, nil, err
		}
		b, _ := json.Marshal(map[string]string{"transaction_id": txID.String()})
		return txID, false, 201, b, nil
	}
	if amountMinor <= 0 {
		return uuid.Nil, false, 0, nil, fmt.Errorf("%w: amount must be > 0", ErrValidation)
	}
	if fromID == toID {
		return uuid.Nil, false, 0, nil, ErrSameAccount
	}
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if len(currency) != 3 {
		return uuid.Nil, false, 0, nil, fmt.Errorf("%w: currency required", ErrValidation)
	}
	switch scope {
	case ScopeTransfer, "hold", "capture", "release", "withdrawal", "deposit", "payment":
	default:
		return uuid.Nil, false, 0, nil, fmt.Errorf("%w: invalid scope", ErrValidation)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, false, 0, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx,
		`SELECT id, owner_id, currency, status, is_system FROM accounts WHERE id IN ($1,$2) ORDER BY id FOR UPDATE`,
		fromID, toID)
	if err != nil {
		return uuid.Nil, false, 0, nil, err
	}
	type acctRow struct {
		id       uuid.UUID
		ownerID  uuid.UUID
		currency string
		status   string
		isSystem bool
	}
	found := map[uuid.UUID]acctRow{}
	for rows.Next() {
		var r acctRow
		if err := rows.Scan(&r.id, &r.ownerID, &r.currency, &r.status, &r.isSystem); err != nil {
			rows.Close()
			return uuid.Nil, false, 0, nil, err
		}
		found[r.id] = r
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return uuid.Nil, false, 0, nil, err
	}
	src, ok := found[fromID]
	if !ok {
		return uuid.Nil, false, 0, nil, fmt.Errorf("%w: source account", ErrNotFound)
	}
	dst, ok := found[toID]
	if !ok {
		return uuid.Nil, false, 0, nil, fmt.Errorf("%w: destination account", ErrNotFound)
	}
	if src.status != "active" || dst.status != "active" {
		return uuid.Nil, false, 0, nil, fmt.Errorf("%w: account not active", ErrValidation)
	}
	if src.currency != currency || dst.currency != currency {
		return uuid.Nil, false, 0, nil, fmt.Errorf("%w: currency mismatch", ErrValidation)
	}

	var srcBalance int64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(CASE WHEN direction='credit' THEN amount ELSE -amount END),0)
		 FROM ledger_entries WHERE account_id=$1 AND status='posted'`, fromID).Scan(&srcBalance); err != nil {
		return uuid.Nil, false, 0, nil, err
	}
	if !src.isSystem && srcBalance < amountMinor {
		return uuid.Nil, false, 0, nil, ErrInsufficientFunds
	}

	userID := src.ownerID
	if callerID, _, ok := CallerFromContext(ctx); ok {
		userID = callerID
	}
	reqHash := hashIdempotencyRequest(userID, scope, key, fromID, toID, amountMinor, currency)
	var dummy uuid.UUID
	err = tx.QueryRow(ctx,
		`INSERT INTO idempotency_keys (user_id, scope, key, status, request_hash)
		 VALUES ($1,$2,$3,'in_progress',$4)
		 ON CONFLICT (user_id, scope, key) DO NOTHING RETURNING user_id`,
		userID, scope, key, reqHash).Scan(&dummy)
	if err != nil {
		if err == pgx.ErrNoRows {
			// Conflict: replay stored response without touching ledger (single consistent view).
			var st, storedHash string
			var storedTx *string
			var storedCode *int
			var storedBody []byte
			err2 := tx.QueryRow(ctx,
				`SELECT status, request_hash, transaction_id::text, response_code, response_body FROM idempotency_keys
				 WHERE user_id=$1 AND scope=$2 AND key=$3`,
				userID, scope, key).Scan(&st, &storedHash, &storedTx, &storedCode, &storedBody)
			if err2 != nil {
				return uuid.Nil, false, 0, nil, err2
			}
			if st == "in_progress" || st == "failed" {
				return uuid.Nil, false, 0, nil, ErrIdempotencyInFlight
			}
			if storedHash != reqHash {
				return uuid.Nil, false, 0, nil, ErrIdempotencyMismatch
			}
			if storedTx == nil {
				return uuid.Nil, false, 0, nil, ErrIdempotencyInFlight
			}
			id, perr := uuid.Parse(*storedTx)
			if perr != nil {
				return uuid.Nil, false, 0, nil, perr
			}
			replayCode := 201
			if storedCode != nil {
				replayCode = *storedCode
			}
			return id, true, replayCode, storedBody, nil
		}
		return uuid.Nil, false, 0, nil, err
	}

	var newTxID uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO transactions (type) VALUES ('transfer') RETURNING id`).Scan(&newTxID); err != nil {
		return uuid.Nil, false, 0, nil, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO ledger_entries (transaction_id, account_id, direction, amount, currency, status)
		 VALUES ($1,$2,'debit',$4,$5,'posted'), ($1,$3,'credit',$4,$5,'posted')`,
		newTxID, fromID, toID, amountMinor, currency); err != nil {
		return uuid.Nil, false, 0, nil, err
	}
	_ = insertOutbox(ctx, tx, "transaction", newTxID, "ledger.transfer.posted", map[string]any{"transaction_id": newTxID, "from": fromID, "to": toID, "amount": amountMinor, "currency": currency})
	respBody, _ := json.Marshal(map[string]string{"transaction_id": newTxID.String()})
	if _, err := tx.Exec(ctx,
		`UPDATE idempotency_keys SET status='completed', transaction_id=$1, response_code=201, response_body=$2
		 WHERE user_id=$3 AND scope=$4 AND key=$5`,
		newTxID, string(respBody), userID, scope, key); err != nil {
		return uuid.Nil, false, 0, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23514" {
			return uuid.Nil, false, 0, nil, fmt.Errorf("%w: %w", ErrInsufficientFunds, err)
		}
		return uuid.Nil, false, 0, nil, err
	}
	return newTxID, false, 201, respBody, nil
}

func hashIdempotencyRequest(userID uuid.UUID, scope, key string, fromID, toID uuid.UUID, amountMinor int64, currency string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s|%s|%d|%s", userID.String(), scope, key, fromID.String(), toID.String(), amountMinor, currency)))
	return hex.EncodeToString(h[:])
}
