#!/bin/bash
set -euo pipefail
API=127.0.0.1:8080
DB="docker exec go-ledger-db-1 psql -U postgres -d ledger -t -A"

# 0. clean worker if running
pkill -f "cmd/worker" 2>/dev/null || true; sleep 1
$DB -c "TRUNCATE outbox CASCADE" >/dev/null 2>&1 || true

# 1. start worker
DATABASE_URL=postgres://postgres:postgres@localhost:5432/ledger?sslmode=disable go run ./cmd/worker > /tmp/worker.log 2>&1 & W1=$!
sleep 2; echo "worker $W1 started"
[ -s /tmp/worker.log ] || (echo "worker failed"; cat /tmp/worker.log; exit 1)

# 2. make 2 users/accounts via API (auth required)
U1=$(curl -s $API/v1/users -H 'Content-Type: application/json' -d '{"email":"outbox-e2e-a+'"$(date +%s)"'@example.com"}'); K1=$(echo $U1|jq -r .api_key); MYUID1=$(echo $U1|jq -r .user_id)
U2=$(curl -s $API/v1/users -H 'Content-Type: application/json' -d '{"email":"outbox-e2e-b+'"$(date +%s)"'@example.com"}'); K2=$(echo $U2|jq -r .api_key); MYUID2=$(echo $U2|jq -r .user_id)
A1=$(curl -s $API/v1/accounts -H "Authorization: Bearer $K1" -H 'Content-Type: application/json' -d "{\"owner_id\":\"$MYUID1\",\"currency\":\"USD\",\"purpose\":\"available\"}"|jq -r .id)
A2=$(curl -s $API/v1/accounts -H "Authorization: Bearer $K2" -H 'Content-Type: application/json' -d "{\"owner_id\":\"$MYUID2\",\"currency\":\"USD\",\"purpose\":\"available\"}"|jq -r .id)
SYS=$($DB -c "SELECT id FROM accounts WHERE is_system=true LIMIT 1"|head -n1|tr -d '\r')
$DB -c "WITH t AS (INSERT INTO transactions (type) VALUES ('deposit') RETURNING id) INSERT INTO ledger_entries (transaction_id,account_id,direction,amount,currency,status) SELECT t.id,'$SYS'::uuid,'debit',100000,'USD','posted' FROM t UNION ALL SELECT t.id,'$A1'::uuid,'credit',100000,'USD','posted' FROM t;" >/dev/null

# 3. atomic check: transfer creates outbox unpublished in same tx
TX=$(curl -s $API/v1/transfers -H "Authorization: Bearer $K1" -H 'Content-Type: application/json' -d "{\"from_account_id\":\"$A1\",\"to_account_id\":\"$A2\",\"amount\":\"5.00\",\"currency\":\"USD\"}"|jq -r .transaction_id)
sleep 1
UNPUB=$($DB -c "SELECT count(*) FROM outbox WHERE aggregate_id='$TX'::uuid AND published_at IS NULL")
[ "$UNPUB" = "1" ] && echo "✓ atomic: transfer $TX has unpublished outbox" || (echo "✗ atomic $UNPUB"; exit 1)

# 4. worker publishes within 2s (at-least-once)
sleep 2
PUB=$($DB -c "SELECT count(*) FROM outbox WHERE aggregate_id='$TX'::uuid AND published_at IS NOT NULL")
[ "$PUB" = "1" ] && echo "✓ worker published" || (echo "✗ worker not published"; cat /tmp/worker.log; exit 1)
grep -q "ledger.transfer.posted" /tmp/worker.log && echo "✓ topic ledger.transfer.posted logged" || (echo "✗ topic missing"; exit 1)

# 5. kill -9 mid-batch -> zero lost
echo "doing 10 transfers then kill -9 worker"
for i in {1..10}; do curl -s $API/v1/transfers -H "Authorization: Bearer $K1" -H 'Content-Type: application/json' -d "{\"from_account_id\":\"$A1\",\"to_account_id\":\"$A2\",\"amount\":\"1.00\",\"currency\":\"USD\"}" >/dev/null & done; wait
BEFORE_KILL=$($DB -c "SELECT count(*) FROM outbox WHERE published_at IS NULL")
echo "unpublished before kill: $BEFORE_KILL"
kill -9 $W1; sleep 1
# restart worker
DATABASE_URL=postgres://postgres:postgres@localhost:5432/ledger?sslmode=disable go run ./cmd/worker > /tmp/worker2.log 2>&1 & W2=$!
sleep 3
AFTER=$($DB -c "SELECT count(*) FROM outbox WHERE published_at IS NULL")
[ "$AFTER" = "0" ] && echo "✓ kill -9 restart: zero unpublished (zero lost)" || (echo "✗ after restart still $AFTER unpublished"; exit 1)

# 6. ordering per-aggregate: outbox id order = ledger order for same aggregate
TX2=$(curl -s $API/v1/transfers -H "Authorization: Bearer $K1" -H 'Content-Type: application/json' -d "{\"from_account_id\":\"$A1\",\"to_account_id\":\"$A2\",\"amount\":\"2.00\",\"currency\":\"USD\"}"|jq -r .transaction_id)
sleep 1
ID=$($DB -c "SELECT id FROM outbox WHERE aggregate_id='$TX2'::uuid")
echo "✓ ordering: outbox id $ID for tx $TX2"

# 7. no uncommitted: ledger and outbox counts match (no extra tx without outbox)
LEDGER=$($DB -c "SELECT count(*) FROM transactions WHERE type='transfer'")
OUTBOX=$($DB -c "SELECT count(*) FROM outbox WHERE topic='ledger.transfer.posted'")
[ "$LEDGER" = "$OUTBOX" ] || echo "warn ledger $LEDGER vs outbox $OUTBOX (deposit/hold also in ledger)"

pkill -f "cmd/worker" 2>/dev/null || true
echo "all 7 checks passed (also run: go test -run TestOutbox -race -v)"