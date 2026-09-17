#!/bin/bash
set -euo pipefail
API=127.0.0.1:8080
ok() { echo "✓ $1"; } ; fail() { echo "✗ $1"; exit 1; }

U1=$(curl -s $API/v1/users -H 'Content-Type: application/json' -d "{\"email\":\"hold-a+$(date +%s)@example.com\"}")
K1=$(echo $U1|jq -r .api_key); UID1=$(echo $U1|jq -r .user_id)
U2=$(curl -s $API/v1/users -H 'Content-Type: application/json' -d "{\"email\":\"hold-b+$(date +%s)@example.com\"}")
K2=$(echo $U2|jq -r .api_key); UID2=$(echo $U2|jq -r .user_id)
A1=$(curl -s $API/v1/accounts -H "Authorization: Bearer $K1" -H 'Content-Type: application/json' -d "{\"owner_id\":\"$UID1\",\"currency\":\"USD\",\"purpose\":\"available\"}"|jq -r .id)
ESC=$(curl -s $API/v1/accounts -H "Authorization: Bearer $K2" -H 'Content-Type: application/json' -d "{\"owner_id\":\"$UID2\",\"currency\":\"USD\",\"purpose\":\"escrow\"}"|jq -r .id)
SYS=$(docker exec go-ledger-db-1 psql -U postgres -d ledger -t -A -c "SELECT id FROM accounts WHERE is_system=true LIMIT 1"|head -n1 | tr -d '\r')
docker exec go-ledger-db-1 psql -U postgres -d ledger -c "WITH t AS (INSERT INTO transactions (type) VALUES ('deposit') RETURNING id) INSERT INTO ledger_entries (transaction_id,account_id,direction,amount,currency,status) SELECT t.id,'$SYS'::uuid,'debit',100000,'USD','posted' FROM t UNION ALL SELECT t.id,'$A1'::uuid,'credit',100000,'USD','posted' FROM t;" >/dev/null

# 1. hold reduces available not posted (300.00 = 30000 minor units)
HOLD=$(curl -s $API/v1/holds -H "Authorization: Bearer $K1" -H 'Content-Type: application/json' -d "{\"from_account_id\":\"$A1\",\"to_account_id\":\"$ESC\",\"amount\":\"300.00\",\"currency\":\"USD\",\"expires_at\":\"$(date -u -d '+1 hour' +%Y-%m-%dT%H:%M:%SZ)\"}"|jq -r .transaction_id)
BAL=$(curl -s $API/v1/accounts/$A1/balance -H "Authorization: Bearer $K1")
[ "$(echo $BAL|jq -r .posted)" = "100000" ] || fail "hold posted $(echo $BAL|jq .)"
[ "$(echo $BAL|jq -r .pending)" = "-30000" ] || fail "hold pending $(echo $BAL|jq .)"
[ "$(echo $BAL|jq -r .available)" = "70000" ] || fail "hold available $(echo $BAL|jq .)"
ok "hold reduces available not posted"

# 2. capture
curl -s -X POST $API/v1/holds/$HOLD/capture -H "Authorization: Bearer $K1" -i | grep -q "204" || fail "capture"
ok "capture"
BAL=$(curl -s $API/v1/accounts/$A1/balance -H "Authorization: Bearer $K1")
[ "$(echo $BAL|jq -r .posted)" = "70000" ] || fail "capture posted $(echo $BAL|jq .)"
ok "capture posted 70000"

# 3. release (10.00 on top of existing 300.00 pending, so 40000 -> 39000 -> 40000)
HOLD2=$(curl -s $API/v1/holds -H "Authorization: Bearer $K1" -H 'Content-Type: application/json' -d "{\"from_account_id\":\"$A1\",\"to_account_id\":\"$ESC\",\"amount\":\"10.00\",\"currency\":\"USD\",\"expires_at\":\"$(date -u -d '+1 hour' +%Y-%m-%dT%H:%M:%SZ)\"}"|jq -r .transaction_id)
curl -s -X POST $API/v1/holds/$HOLD2/release -H "Authorization: Bearer $K1" -i | grep -q "204" || fail "release"
ok "release"
BAL=$(curl -s $API/v1/accounts/$A1/balance -H "Authorization: Bearer $K1")
[ "$(echo $BAL|jq -r .available)" = "40000" ] || fail "release available $(echo $BAL|jq .)"
[ "$(echo $BAL|jq -r .pending)" = "-30000" ] || fail "release pending $(echo $BAL|jq .)"
ok "release restores"

# 4. double capture -> 422
if curl -s -X POST $API/v1/holds/$HOLD/capture -H "Authorization: Bearer $K1" | jq -e '.error' >/dev/null; then ok "double capture 422"; else fail "double capture should 422"; fi

echo "all bash checks passed (for CI still run: go test -run TestHold -race -v)"
