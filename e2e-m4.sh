#!/bin/bash
set -euo pipefail
API=localhost:8080
OWNER_ID=$(docker compose exec -T db psql -U postgres -d ledger -t -A -c "INSERT INTO users (email) VALUES ('m4+$(date +%s)@example.com') RETURNING id;" | head -n1 | tr -d '\r')
OWNER2=$(docker compose exec -T db psql -U postgres -d ledger -t -A -c "INSERT INTO users (email) VALUES ('m4b+$(date +%s)@example.com') RETURNING id;" | head -n1 | tr -d '\r')
SRC=$(curl -s $API/v1/accounts -H 'Content-Type: application/json' -d "{\"owner_id\":\"$OWNER_ID\",\"currency\":\"USD\",\"purpose\":\"available\"}" | jq -r .id)
DST=$(curl -s $API/v1/accounts -H 'Content-Type: application/json' -d "{\"owner_id\":\"$OWNER2\",\"currency\":\"USD\",\"purpose\":\"available\"}" | jq -r .id)
SYS=$(docker compose exec -T db psql -U postgres -d ledger -t -A -c "SELECT id FROM accounts WHERE is_system=true AND currency='USD' LIMIT 1;" | head -n1 | tr -d '\r')
docker compose exec -T db psql -U postgres -d ledger -c "WITH t AS (INSERT INTO transactions (type) VALUES ('deposit') RETURNING id) INSERT INTO ledger_entries (transaction_id, account_id, direction, amount, currency, status) SELECT t.id,'$SYS'::uuid,'debit',100000,'USD','posted' FROM t UNION ALL SELECT t.id,'$SRC'::uuid,'credit',100000,'USD','posted' FROM t;" >/dev/null
echo "SRC=$SRC DST=$DST"
curl -s $API/v1/accounts/$SRC/balance | jq
for i in 1 2 3; do curl -s $API/v1/transfers -H 'Content-Type: application/json' -d "{\"from_account_id\":\"$SRC\",\"to_account_id\":\"$DST\",\"amount\":\"10.00\",\"currency\":\"USD\"}" | jq; done
curl -s "$API/v1/accounts/$SRC/statement?limit=5" | jq
TX=$(curl -s $API/v1/transfers -H 'Content-Type: application/json' -d "{\"from_account_id\":\"$SRC\",\"to_account_id\":\"$DST\",\"amount\":\"1.00\",\"currency\":\"USD\"}" | jq -r .transaction_id)
curl -s $API/v1/transactions/$TX | jq