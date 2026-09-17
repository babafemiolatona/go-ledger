-- +goose Up
CREATE TABLE api_keys (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id),
  key_hash TEXT UNIQUE NOT NULL,
  key_prefix TEXT NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('user','service')) DEFAULT 'user',
  status TEXT NOT NULL CHECK (status IN ('active','revoked')) DEFAULT 'active',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  revoked_at TIMESTAMPTZ,
  last_used_at TIMESTAMPTZ
);
-- UNIQUE already indexes key_hash

-- +goose Down
DROP TABLE IF EXISTS api_keys;