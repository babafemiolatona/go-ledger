-- +goose Up
CREATE TABLE reconciliation_runs (
  id BIGSERIAL PRIMARY KEY,
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  finished_at TIMESTAMPTZ,
  drift BIGINT NOT NULL DEFAULT 0,
  status TEXT NOT NULL CHECK (status IN ('ok','drift'))
);
-- +goose Down
DROP TABLE IF EXISTS reconciliation_runs;