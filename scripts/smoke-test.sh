#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${1:-http://localhost:8080}"
RETRIES="${2:-3}"
DELAY="${3:-5}"

echo "Running smoke tests against $BASE_URL"

check() {
  local name="$1" url="$2" match="$3"
  for i in $(seq 1 "$RETRIES"); do
    if curl -sf --max-time 10 "$url" | grep -q "$match"; then
      echo "  [PASS] $name"
      return 0
    fi
    if [ "$i" -lt "$RETRIES" ]; then
      echo "  [RETRY] $name (attempt $i/$RETRIES)"
      sleep "$DELAY"
    fi
  done
  echo "  [FAIL] $name"
  return 1
}

failed=0

check "Health endpoint" "$BASE_URL/api/v1/health" '"status"' || ((failed++))
check "Features endpoint" "$BASE_URL/api/v1/features" '"bookings"' || ((failed++))
check "Frontend loads" "$BASE_URL/" '<div id="app">' || ((failed++))

echo ""
if [ "$failed" -eq 0 ]; then
  echo "All smoke tests passed"
else
  echo "$failed smoke test(s) failed"
  exit 1
fi
