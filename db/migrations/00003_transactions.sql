-- +goose Up
CREATE TABLE transactions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  type TEXT NOT NULL CHECK (type IN ('payment', 'transfer', 'withdrawal', 'deposit', 'hold')),
  expires_at TIMESTAMPTZ NULL,
  metadata JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX transactions_expires_at_partial
  ON transactions (expires_at)
  WHERE type = 'hold' AND expires_at IS NOT NULL;

-- +goose Down
DROP TABLE IF EXISTS transactions;
