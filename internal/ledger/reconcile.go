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

	rows, _ := s.pool.Query(ctx, `SELECT transaction_id::text, SUM(CASE WHEN direction='credit' THEN amount ELSE -amount END) FROM ledger_entries WHERE status='posted' GROUP BY transaction_id HAVING SUM(CASE WHEN direction='credit' THEN amount ELSE -amount END) !=0`)
	for rows.Next() {
		var id string
		var d int64
		rows.Scan(&id, &d)
		r.PerTxDrift[id] = d
	}
	rows.Close()
	rows, _ = s.pool.Query(ctx, `SELECT account_id::text, SUM(CASE WHEN direction='credit' THEN amount ELSE -amount END) FROM ledger_entries WHERE status='posted' GROUP BY account_id`)
	for rows.Next() {
		var id string
		var d int64
		rows.Scan(&id, &d)
		r.PerAcctDrift[id] = d
	}
	rows.Close()
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
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	n := 0
	for _, id := range ids {
		var pending int
		s.pool.QueryRow(ctx, `SELECT count(*) FROM ledger_entries WHERE transaction_id=$1::uuid AND status='pending'`, id).Scan(&pending)
		if pending == 0 {
			continue
		}

		var posted int
		s.pool.QueryRow(ctx, `SELECT count(*) FROM ledger_entries WHERE transaction_id=$1::uuid AND status='posted'`, id).Scan(&posted)
		if posted > 0 {
			continue
		}
		var rev int
		s.pool.QueryRow(ctx, `SELECT count(*) FROM ledger_entries WHERE transaction_id=$1::uuid AND status='pending' AND direction='credit'`).Scan(&rev)
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
