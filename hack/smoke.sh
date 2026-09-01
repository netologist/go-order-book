#!/usr/bin/env bash
#
# smoke.sh — API smoke test for the Order Book order-book server.
#
# Usage:
#   ./hack/smoke.sh              # Start server, run tests, stop
#   ./hack/smoke.sh --test-only  # Run tests against an already-running server
#   PORT=9000 ./hack/smoke.sh    # Custom port
#
# Depends on: curl, python3.

set -euo pipefail

cd "$(dirname "$0")/.."

PORT="${PORT:-8080}"
BASE="http://localhost:${PORT}"

PASS=0
FAIL=0
TEST=""

ok() {
	PASS=$((PASS + 1))
	echo "  ✅ ${TEST}: $1"
}
fail() {
	FAIL=$((FAIL + 1))
	echo "  ❌ ${TEST}: $1"
}

# ---------------------------------------------------------------------------
# helpers
# ---------------------------------------------------------------------------

assert_status() {
	local want="$1" label="$2"
	shift 2
	local code
	code=$(curl -s -o /dev/null -w "%{http_code}" "$@")
	if [ "$code" = "$want" ]; then
		ok "$label (HTTP $code)"
	else
		fail "$label: want HTTP ${want}, got ${code}"
	fi
}

# assert_json extracts a value from JSON via python3 and compares it.
# Filters are Python dict/list access expressions, e.g. "['status']"
assert_json() {
	local expr="$1" expected="$2" body="$3" label="${4:-}"
	local val
	val=$(echo "$body" | python3 -c "
import sys, json
try:
    r = json.load(sys.stdin)
    print(r${expr})
except Exception as e:
    print(f'<error: {e}>')
" 2>/dev/null || echo "<parse-error>")
	if [ "$val" = "$expected" ]; then
		ok "${label} -> ${val}"
	else
		fail "${label}: want ${expected}, got ${val}"
	fi
}

# request_once makes one HTTP request and returns the body.
request_once() {
	local method="$1" path="$2"
	shift 2
	curl -sf -X "$method" "${BASE}${path}" "$@" 2>/dev/null || echo ""
}

# ---------------------------------------------------------------------------
# server lifecycle
# ---------------------------------------------------------------------------

if [ "${1:-}" != "--test-only" ]; then
	echo "### Starting server on :${PORT} ..."
	LOG_FORMAT=text go run ./cmd/server &
	SERVER_PID=$!
	trap 'kill $SERVER_PID 2>/dev/null; wait $SERVER_PID 2>/dev/null; echo "### Stopped."' EXIT

	for _ in $(seq 1 20); do
		if curl -sf "${BASE}/healthz" >/dev/null 2>&1; then break; fi
		sleep 0.3
	done
fi

echo ""
echo "### Smoke tests"
echo ""

# ===========================================================================
# 1. Health
# ===========================================================================
echo "-- Health --"
H=$(request_once GET /healthz)
TEST="health"
assert_status 200 "status" "${BASE}/healthz"
assert_json "['status']" "ok" "$H"
echo ""

# ===========================================================================
# 2. Place + match (price improvement)
# ===========================================================================
echo "-- Place & match --"

R=$(request_once POST /orders \
	-H "Content-Type: application/json" \
	-d '{"user_id":"alice","side":"sell","type":"limit","price":64,"quantity":10}')

TEST="sell"
# order_id should be present and non-empty
echo "$R" | python3 -c "import sys,json; r=json.load(sys.stdin); assert len(r['order_id'])>0" && ok "order_id present"
assert_json "['remaining_qty']" "10" "$R"

R=$(request_once POST /orders \
	-H "Content-Type: application/json" \
	-d '{"user_id":"bob","side":"buy","type":"limit","price":65,"quantity":10}')

TEST="match"
# trades list should be present
assert_json "['trades'][0]['price']" "64" "$R" "trade price"
assert_json "['trades'][0]['price']" "64" "$R" # price improvement
assert_json "['trades'][0]['quantity']" "10" "$R"
echo ""

# ===========================================================================
# 3. Market order
# ===========================================================================
echo "-- Market order --"
request_once POST /orders \
	-H "Content-Type: application/json" \
	-d '{"user_id":"alice","side":"sell","type":"limit","price":60,"quantity":5}' >/dev/null

R=$(request_once POST /orders \
	-H "Content-Type: application/json" \
	-d '{"user_id":"bob","side":"buy","type":"market","price":0,"quantity":5}')

TEST="market"
assert_json "['trades'][0]['price']" "60" "$R"
assert_json "['trades'][0]['quantity']" "5" "$R"
echo ""

# ===========================================================================
# 4. IOC (Immediate-or-Cancel)
# ===========================================================================
echo "-- IOC --"
request_once POST /orders \
	-H "Content-Type: application/json" \
	-d '{"user_id":"alice","side":"sell","type":"limit","price":70,"quantity":10}' >/dev/null

R=$(request_once POST /orders \
	-H "Content-Type: application/json" \
	-d '{"user_id":"bob","side":"buy","type":"ioc","price":65,"quantity":5}')

TEST="ioc no-cross"
# IOC with no cross: trades should be empty list
R_IOC_TRADES=$(echo "$R" | python3 -c "import sys,json; print(json.load(sys.stdin).get('trades',[]))")
[ "$R_IOC_TRADES" = "[]" ] && ok "no trades (empty list)" || fail "trades = $R_IOC_TRADES, want []"
echo ""

# ===========================================================================
# 5. Validation errors
# ===========================================================================
echo "-- Validation --"
TEST="validation"
assert_status 400 "empty user_id" "${BASE}/orders" -X POST \
	-H "Content-Type: application/json" -d '{"user_id":"","side":"buy","type":"limit","price":50,"quantity":1}'
assert_status 400 "zero quantity" "${BASE}/orders" -X POST \
	-H "Content-Type: application/json" -d '{"user_id":"alice","side":"buy","type":"limit","price":50,"quantity":0}'
assert_status 400 "invalid side" "${BASE}/orders" -X POST \
	-H "Content-Type: application/json" -d '{"user_id":"alice","side":"hold","type":"limit","price":50,"quantity":1}'
assert_status 400 "market w/price" "${BASE}/orders" -X POST \
	-H "Content-Type: application/json" -d '{"user_id":"alice","side":"buy","type":"market","price":70,"quantity":1}'
echo ""

# ===========================================================================
# 6. Cancel
# ===========================================================================
echo "-- Cancel --"
R=$(request_once POST /orders \
	-H "Content-Type: application/json" \
	-d '{"user_id":"alice","side":"sell","type":"limit","price":55,"quantity":10}')
OID=$(echo "$R" | python3 -c "import sys,json; print(json.load(sys.stdin)['order_id'])" 2>/dev/null || echo "")

TEST="cancel"
assert_status 200 "delete ok" "${BASE}/orders/${OID}" -X DELETE
assert_status 404 "not found" "${BASE}/orders/nonexistent" -X DELETE
echo ""

# ===========================================================================
# 7. Idempotency
# ===========================================================================
echo "-- Idempotency --"
R1=$(request_once POST /orders \
	-H "Content-Type: application/json" -H "Idempotency-Key: smoke-1" \
	-d '{"user_id":"alice","side":"sell","type":"limit","price":80,"quantity":3}')
OID1=$(echo "$R1" | python3 -c "import sys,json; print(json.load(sys.stdin)['order_id'])" 2>/dev/null || echo "")

R2=$(request_once POST /orders \
	-H "Content-Type: application/json" -H "Idempotency-Key: smoke-1" \
	-d '{"user_id":"alice","side":"sell","type":"limit","price":80,"quantity":3}')
OID2=$(echo "$R2" | python3 -c "import sys,json; print(json.load(sys.stdin)['order_id'])" 2>/dev/null || echo "")

TEST="idempotency"
if [ "$OID1" = "$OID2" ] && [ -n "$OID1" ]; then
	ok "same order_id on replay -> ${OID1}"
else
	fail "order_ids differ (${OID1} vs ${OID2})"
fi
echo ""

# ===========================================================================
# Summary
# ===========================================================================
echo "========================================"
echo "  ${PASS} passed, ${FAIL} failed"
echo "========================================"

[ "$FAIL" -eq 0 ]
