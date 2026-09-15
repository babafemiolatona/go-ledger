-- +goose Up
CREATE TABLE idempotency_keys (
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key TEXT NOT NULL,
  scope TEXT NOT NULL CHECK (scope IN ('transfer','hold','capture','release','withdrawal','deposit','payment')),
  transaction_id UUID NULL REFERENCES transactions(id) ON DELETE SET NULL,
  status TEXT NOT NULL CHECK (status IN ('in_progress','completed','failed')),
  request_hash TEXT NOT NULL,
  response_code INT NULL,
  response_body JSONB NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, scope, key)
);

CREATE INDEX idempotency_keys_status_created_at ON idempotency_keys (status, created_at);

CREATE TRIGGER idempotency_keys_updated_at
BEFORE UPDATE ON idempotency_keys
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TRIGGER IF EXISTS idempotency_keys_updated_at ON idempotency_keys;
DROP TABLE IF EXISTS idempotency_keys;