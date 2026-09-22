#!/bin/bash
# M8(a) webhook delivery E2E: happy-path delivery + HMAC verify + retry + reconcile==0.
# Dead-letter/backoff-curve are covered by: go test -run 'TestWebhooks_DeadLetter|TestWebhooks_Backoff' -race -v
set -euo pipefail
API=127.0.0.1:8080
export DATABASE_URL=${DATABASE_URL:-postgres://postgres:postgres@localhost:5432/ledger?sslmode=disable}
PSQL="psql $DATABASE_URL -t -A"

# NOTE: `go run` compiles workers to go-build paths, so match those too (never
# pkill -f "e2e-webhooks": the pattern would match this shell itself).
pkill -f "cmd/worker" 2>/dev/null || true
pkill -f "go-build.*-d/worker" 2>/dev/null || true
pkill -f "wh_receiver.py" 2>/dev/null || true; sleep 1
$PSQL -c "TRUNCATE webhook_deliveries, webhook_endpoints, outbox CASCADE" >/dev/null

# 1. register endpoint (capture one-time secret), then start verifying receiver
U=$(curl -s -m 15 $API/v1/users -H 'Content-Type: application/json' -d '{"email":"wh-e2e-'"$(date +%s)"'@example.com"}')
K=$(echo $U|jq -r .api_key); MYUID=$(echo $U|jq -r .user_id)
EP=$(curl -s -m 15 $API/v1/webhooks -H "Authorization: Bearer $K" -H 'Content-Type: application/json' -d '{"url":"http://127.0.0.1:8099/hook"}')
EPID=$(echo $EP|jq -r .id); SECRET=$(echo $EP|jq -r .secret)
[ -n "$EPID" ] && [ "$SECRET" != "null" ] && [ -n "$SECRET" ] || (echo "✗ register failed: $EP"; exit 1)
echo "✓ webhook $EPID registered, secret returned once"

cat > /tmp/wh_receiver.py <<'PYEOF'
import hmac, hashlib, os
from http.server import BaseHTTPRequestHandler, HTTPServer
SECRET = os.environ["WH_SECRET"].encode()
LOG = "/tmp/wh.log"
class H(BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(n)
        sig = self.headers.get("X-Webhook-Signature", "")
        want = "sha256=" + hmac.new(SECRET, body, hashlib.sha256).hexdigest()
        ok = hmac.compare_digest(sig, want)
        topic = self.headers.get("X-Webhook-Topic", "?")
        with open(LOG, "a") as f:
            f.write(("OK " if ok else "BAD-SIG ") + topic + "\n")
        self.send_response(200); self.end_headers()
    def log_message(self, *a): pass
HTTPServer(("127.0.0.1", 8099), H).serve_forever()
PYEOF
rm -f /tmp/wh.log
WH_SECRET="$SECRET" python3 /tmp/wh_receiver.py </dev/null >/tmp/wh_receiver.log 2>&1 & RXPID=$!
sleep 1

# 2. start worker (outbox publish + fanout + 5s delivery loop)
go run ./cmd/worker </dev/null >/tmp/worker-wh.log 2>&1 & W1=$!
sleep 2

# 3. fund + transfer -> outbox -> fanout -> delivery
A1=$(curl -s -m 15 $API/v1/accounts -H "Authorization: Bearer $K" -H 'Content-Type: application/json' -d "{\"owner_id\":\"$MYUID\",\"currency\":\"USD\",\"purpose\":\"available\"}"|jq -r .id)
U2=$(curl -s -m 15 $API/v1/users -H 'Content-Type: application/json' -d '{"email":"wh-e2e-b-'"$(date +%s)"'@example.com"}'); K2=$(echo $U2|jq -r .api_key); MYUID2=$(echo $U2|jq -r .user_id)
A2=$(curl -s -m 15 $API/v1/accounts -H "Authorization: Bearer $K2" -H 'Content-Type: application/json' -d "{\"owner_id\":\"$MYUID2\",\"currency\":\"USD\",\"purpose\":\"available\"}"|jq -r .id)
# system funding account (tests truncate everything, so ensure one exists)
# NOTE: psql -t does not suppress INSERT tags, so extract UUIDs by pattern.
UUIDRE='[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}'
# (grep with no match exits 1: neutralize with || true under set -e+pipefail)
SYSU=$($PSQL -c "INSERT INTO users (email) VALUES ('wh-e2e-sys@example.com') ON CONFLICT DO NOTHING RETURNING id"|grep -E -o "$UUIDRE"|head -n1 || true)
[ -z "$SYSU" ] && SYSU=$($PSQL -c "SELECT id FROM users WHERE email='wh-e2e-sys@example.com'"|grep -E -o "$UUIDRE"|head -n1 || true)
SYS=$($PSQL -c "SELECT id FROM accounts WHERE owner_id='$SYSU'::uuid AND is_system LIMIT 1"|grep -E -o "$UUIDRE"|head -n1 || true)
if [ -z "$SYS" ]; then SYS=$($PSQL -c "INSERT INTO accounts (owner_id, currency, purpose, is_system) VALUES ('$SYSU'::uuid,'USD','available',TRUE) RETURNING id"|grep -E -o "$UUIDRE"|head -n1 || true); fi
echo "funding via system account $SYS"
$PSQL -c "WITH t AS (INSERT INTO transactions (type) VALUES ('deposit') RETURNING id) INSERT INTO ledger_entries (transaction_id,account_id,direction,amount,currency,status) SELECT t.id,'$SYS'::uuid,'debit',100000,'USD','posted' FROM t UNION ALL SELECT t.id,'$A1'::uuid,'credit',100000,'USD','posted' FROM t;" >/dev/null
curl -s -m 15 $API/v1/transfers -H "Authorization: Bearer $K" -H 'Content-Type: application/json' -d "{\"from_account_id\":\"$A1\",\"to_account_id\":\"$A2\",\"amount\":\"5.00\",\"currency\":\"USD\"}" >/dev/null

sleep 8
ST=$($PSQL -c "SELECT status FROM webhook_deliveries WHERE endpoint_id='$EPID'::uuid"|head -n1|tr -d '\r')
[ "$ST" = "delivered" ] && echo "✓ delivery row = delivered" || (echo "✗ status $ST"; tail -5 /tmp/worker-wh.log; exit 1)
grep -q "^OK ledger.transfer.posted$" /tmp/wh.log && echo "✓ receiver got ledger.transfer.posted with valid HMAC" || (echo "✗ receiver log:"; cat /tmp/wh.log; exit 1)
grep -q "BAD-SIG" /tmp/wh.log && (echo "✗ bad signature seen"; exit 1) || echo "✓ no BAD-SIG lines"

# 4. retry: endpoint on dead port -> attempts increment, stays queued, last_error set
EPBAD=$(curl -s -m 15 $API/v1/webhooks -H "Authorization: Bearer $K" -H 'Content-Type: application/json' -d '{"url":"http://127.0.0.1:9/hook"}'|jq -r .id)
curl -s -m 15 $API/v1/transfers -H "Authorization: Bearer $K" -H 'Content-Type: application/json' -d "{\"from_account_id\":\"$A1\",\"to_account_id\":\"$A2\",\"amount\":\"1.00\",\"currency\":\"USD\"}" >/dev/null
sleep 8
ROW=$($PSQL -c "SELECT status||'|'||attempts||'|'||(last_error IS NOT NULL) FROM webhook_deliveries WHERE endpoint_id='$EPBAD'::uuid ORDER BY id DESC LIMIT 1"|tr -d '\r')
echo "dead-port delivery: $ROW"
echo "$ROW" | grep -q "^queued|[1-9]" || (echo "✗ no retry on dead port"; exit 1)
echo "$ROW" | grep -q -E "\|(t|true)$" && echo "✓ retry recorded with last_error, still queued" || (echo "✗ last_error missing"; exit 1)

# 5. reconcile still zero after exercising webhooks
go run ./cmd/reconcile >/tmp/reconcile-wh.log 2>&1; RC=$?
[ "$RC" = "0" ] && echo "✓ reconcile exit 0 (no drift)" || (echo "✗ reconcile rc=$RC"; cat /tmp/reconcile-wh.log; exit 1)

kill $W1 $RXPID 2>/dev/null || true
pkill -f "cmd/worker" 2>/dev/null || true
pkill -f "go-build.*-d/worker" 2>/dev/null || true
pkill -f "wh_receiver.py" 2>/dev/null || true
echo "e2e-webhooks: all checks passed"
