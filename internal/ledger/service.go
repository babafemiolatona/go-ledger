package ledger

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

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
	b.Available = posted
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

	ids := []uuid.UUID{fromID, toID}
	if ids[0].String() > ids[1].String() {
		ids[0], ids[1] = ids[1], ids[0]
	}
	rows, err := tx.Query(ctx,
		`SELECT id, currency, status, is_system FROM accounts WHERE id IN ($1,$2) ORDER BY id FOR UPDATE`,
		fromID, toID)
	if err != nil {
		return uuid.Nil, err
	}
	type acctRow struct {
		id       uuid.UUID
		currency string
		status   string
		isSystem bool
	}
	found := map[uuid.UUID]acctRow{}
	for rows.Next() {
		var r acctRow
		if err := rows.Scan(&r.id, &r.currency, &r.status, &r.isSystem); err != nil {
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

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return txID, nil
}
