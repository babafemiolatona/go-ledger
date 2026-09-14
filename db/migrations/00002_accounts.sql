-- +goose Up
CREATE TABLE accounts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_id UUID NOT NULL REFERENCES users(id),
  currency CHAR(3) NOT NULL,
  purpose TEXT NOT NULL CHECK (purpose IN ('available', 'escrow', 'fees')),
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'closed', 'frozen')),
  is_system BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX accounts_owner_currency_purpose_uniq
  ON accounts (owner_id, currency, purpose);

CREATE TRIGGER accounts_updated_at
BEFORE UPDATE ON accounts
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TRIGGER IF EXISTS accounts_updated_at ON accounts;
DROP TABLE IF EXISTS accounts;
