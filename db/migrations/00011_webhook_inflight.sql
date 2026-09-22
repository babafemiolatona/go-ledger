-- +goose Up
-- Atomic delivery claim (see DeliverDueWebhooks): a worker claims due rows with
-- UPDATE ... FOR UPDATE SKIP LOCKED ... RETURNING in a single statement, which
-- flips them to 'in_flight' with a lease in next_retry_at. 'in_flight' rows whose
-- lease has expired (next_retry_at <= now()) are reclaimable — that is the crash
-- recovery path: if a worker dies mid-delivery, its rows become due again once
-- the lease lapses, so no row can get permanently stuck.
ALTER TABLE webhook_deliveries DROP CONSTRAINT IF EXISTS webhook_deliveries_status_check;
ALTER TABLE webhook_deliveries ADD CONSTRAINT webhook_deliveries_status_check CHECK (status IN ('queued','in_flight','delivered','dead'));
DROP INDEX IF EXISTS webhook_deliveries_due_idx;
CREATE INDEX webhook_deliveries_due_idx ON webhook_deliveries(status, next_retry_at) WHERE status IN ('queued','in_flight');

-- +goose Down
UPDATE webhook_deliveries SET status='queued' WHERE status='in_flight';
ALTER TABLE webhook_deliveries DROP CONSTRAINT IF EXISTS webhook_deliveries_status_check;
ALTER TABLE webhook_deliveries ADD CONSTRAINT webhook_deliveries_status_check CHECK (status IN ('queued','delivered','dead'));
DROP INDEX IF EXISTS webhook_deliveries_due_idx;
CREATE INDEX webhook_deliveries_due_idx ON webhook_deliveries(status, next_retry_at) WHERE status='queued';
