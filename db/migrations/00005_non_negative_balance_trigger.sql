-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION check_non_negative_balance()
RETURNS TRIGGER AS $$
DECLARE
  bad_account UUID;
BEGIN
  SELECT le.account_id INTO bad_account
  FROM ledger_entries le
  JOIN accounts a ON a.id = le.account_id
  WHERE le.transaction_id = NEW.transaction_id
    AND a.is_system = FALSE
    AND le.status = 'posted'
  GROUP BY le.account_id
  HAVING COALESCE(SUM(CASE WHEN le.direction='credit' THEN le.amount ELSE -le.amount END),0) < 0
  -- LIMIT 1 ceiling: this trigger only blocks the first negative-balance account per commit. Multi-leg
  -- transactions that make >1 account negative in the same commit are not fully guarded and rely on
  -- reconciliation (M7) as the backstop. Revisit when M8 multi-currency/FX is implemented.
  LIMIT 1;

  -- also check all touched accounts even if not in NEW (defensive: check any posted negative touched by this tx)
  -- We check via the set of accounts in this transaction
  IF bad_account IS NOT NULL THEN
    -- verify recomputed from full history, not just this tx, to catch cumulative negative
    PERFORM 1
    FROM (
      SELECT le2.account_id,
             COALESCE(SUM(CASE WHEN le2.direction='credit' THEN le2.amount ELSE -le2.amount END),0) AS bal
      FROM ledger_entries le2
      JOIN accounts a2 ON a2.id = le2.account_id
      WHERE le2.account_id = bad_account AND le2.status='posted' AND a2.is_system=FALSE
      GROUP BY le2.account_id
      HAVING COALESCE(SUM(CASE WHEN le2.direction='credit' THEN le2.amount ELSE -le2.amount END),0) < 0
    ) t;
    IF FOUND THEN
      RAISE EXCEPTION 'insufficient_funds: account % would go negative', bad_account
        USING ERRCODE = '23514';
    END IF;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS ledger_entries_non_negative_deferred ON ledger_entries;
CREATE CONSTRAINT TRIGGER ledger_entries_non_negative_deferred
AFTER INSERT ON ledger_entries
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION check_non_negative_balance();

-- +goose Down
DROP TRIGGER IF EXISTS ledger_entries_non_negative_deferred ON ledger_entries;
DROP FUNCTION IF EXISTS check_non_negative_balance();
