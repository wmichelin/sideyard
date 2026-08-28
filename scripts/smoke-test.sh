#!/usr/bin/env bash
# Smoke-test a deployed ClubDue environment (staging or production).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/env.sh
source "${SCRIPT_DIR}/env.sh"

TARGET="${1:-staging}"
clubdue_env "$TARGET"

echo "Smoke-testing ${CLUBDUE_ENV} at ${CLUBDUE_URL} ..."

curl -fsS "${CLUBDUE_URL}/healthz" | grep -qx ok
echo "  ✓ /healthz"

if [ "$TARGET" = "staging" ]; then
  curl -fsS -X POST "${CLUBDUE_URL}/dev/seed" >/dev/null
  COOKIE_JAR="$(mktemp)"
  trap 'rm -f "$COOKIE_JAR"' EXIT
  LOGIN_STATUS="$(curl -fsS -c "$COOKIE_JAR" -o /dev/null -w '%{http_code}' -d 'email=owner@demo.local&password=demo1234' "${CLUBDUE_URL}/login")"
  [ "$LOGIN_STATUS" = "303" ]
  echo "  ✓ staging login"
  HTML="$(curl -fsS -b "$COOKIE_JAR" "${CLUBDUE_URL}/dashboard")"
else
  HTML="$(curl -fsS "${CLUBDUE_URL}/")"
fi
echo "$HTML" | grep -q 'ClubDue'
echo "  ✓ dashboard title"

if [ "$TARGET" = "staging" ]; then
  echo "$HTML" | grep -q 'Active agreements'
  echo "  ✓ dashboard metrics"
  curl -fsS -b "$COOKIE_JAR" "${CLUBDUE_URL}/agreements" | grep -q 'Alex Rivera'
  echo "  ✓ seeded agreement book"
  curl -fsS -b "$COOKIE_JAR" "${CLUBDUE_URL}/renewals" | grep -q 'Renewal queue'
  echo "  ✓ renewal queue"
  curl -fsS -b "$COOKIE_JAR" "${CLUBDUE_URL}/visits/export.csv" | head -1 | grep -q 'customer_name'
  echo "  ✓ visit CSV export"
else
  echo "$HTML" | grep -q 'Alex Rivera'
  echo "  ✓ sample member row"
  echo "$HTML" | grep -q 'Collected'
  echo "  ✓ summary stats"
fi

echo "All checks passed for ${CLUBDUE_URL}"
