package ledger

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type ReconcileResult struct {
	GlobalDrift  int64
	PerTxDrift   map[string]int64
	PerAcctDrift map[string]int64
	Status       string
}

func (s *Service) Reconcile(ctx context.Context) (ReconcileResult, error) {
	var r ReconcileResult
	r.PerTxDrift = map[string]int64{}
	r.PerAcctDrift = map[string]int64{}

	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(SUM(CASE WHEN direction='credit' THEN amount ELSE -amount END),0) FROM ledger_entries WHERE status='posted'`).Scan(&r.GlobalDrift); err != nil {
		return r, err
	}

	rows, err := s.pool.Query(ctx, `SELECT transaction_id::text, SUM(CASE WHEN direction='credit' THEN amount ELSE -amount END) FROM ledger_entries WHERE status='posted' GROUP BY transaction_id HAVING SUM(CASE WHEN direction='credit' THEN amount ELSE -amount END) !=0`)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var id string
		var d int64
		if err := rows.Scan(&id, &d); err != nil {
			rows.Close()
			return r, err
		}
		r.PerTxDrift[id] = d
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return r, err
	}
	rows, err = s.pool.Query(ctx, `SELECT account_id::text, SUM(CASE WHEN direction='credit' THEN amount ELSE -amount END) FROM ledger_entries WHERE status='posted' GROUP BY account_id`)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var id string
		var d int64
		if err := rows.Scan(&id, &d); err != nil {
			rows.Close()
			return r, err
		}
		r.PerAcctDrift[id] = d
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return r, err
	}
	if r.GlobalDrift == 0 && len(r.PerTxDrift) == 0 {
		r.Status = "ok"
	} else {
		r.Status = "drift"
	}
	return r, nil
}

func (s *Service) SweepExpiredHolds(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM transactions WHERE type='hold' AND expires_at IS NOT NULL AND expires_at < now()`)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		var pending int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM ledger_entries WHERE transaction_id=$1::uuid AND status='pending'`, id).Scan(&pending); err != nil {
			return n, err
		}
		if pending == 0 {
			continue
		}

		var posted int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM ledger_entries WHERE transaction_id=$1::uuid AND status='posted'`, id).Scan(&posted); err != nil {
			return n, err
		}
		if posted > 0 {
			continue
		}
		var rev int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM ledger_entries WHERE transaction_id=$1::uuid AND status='pending' AND direction='credit'`, id).Scan(&rev); err != nil {
			return n, err
		}
		if rev > 1 {
			continue
		}
		if err := s.Release(ctx, parseUUID(id)); err == nil {
			n++
		} else if err == pgx.ErrNoRows {
			continue
		}
	}
	return n, nil
}
func parseUUID(s string) (u uuid.UUID) {
	u, _ = uuid.Parse(s)
	return
}
