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
curl -fsS "${CLUBDUE_URL}/livez" | grep -qx ok
echo "  ✓ /livez"
curl -fsS "${CLUBDUE_URL}/readyz" | grep -qx ok
echo "  ✓ /readyz"

if [ "$TARGET" = "staging" ]; then
  COOKIE_JAR="$(mktemp)"
  trap 'rm -f "$COOKIE_JAR"' EXIT
  LOGIN_PAGE="$(curl -fsS -c "$COOKIE_JAR" "${CLUBDUE_URL}/login")"
  CSRF_TOKEN="$(printf '%s' "$LOGIN_PAGE" | sed -n 's/.*name="csrf_token" value="\([^"]*\)".*/\1/p' | head -1)"
  [ -n "$CSRF_TOKEN" ]
  curl -fsS -b "$COOKIE_JAR" -c "$COOKIE_JAR" -X POST \
    --data-urlencode "csrf_token=$CSRF_TOKEN" "${CLUBDUE_URL}/dev/seed" >/dev/null
  LOGIN_STATUS="$(curl -fsS -b "$COOKIE_JAR" -c "$COOKIE_JAR" -o /dev/null -w '%{http_code}' \
    --data-urlencode "csrf_token=$CSRF_TOKEN" \
    --data-urlencode 'email=owner@demo.local' \
    --data-urlencode 'password=demo1234' "${CLUBDUE_URL}/login")"
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
  AGREEMENTS_HTML="$(curl -fsS -b "$COOKIE_JAR" "${CLUBDUE_URL}/agreements")"
  [[ "$AGREEMENTS_HTML" == *'Alex Rivera'* ]]
  echo "  ✓ seeded agreement book"
  RENEWALS_HTML="$(curl -fsS -b "$COOKIE_JAR" "${CLUBDUE_URL}/renewals")"
  [[ "$RENEWALS_HTML" == *'Renewal queue'* ]]
  echo "  ✓ renewal queue"
  VISITS_CSV="$(curl -fsS -b "$COOKIE_JAR" "${CLUBDUE_URL}/visits/export.csv")"
  [[ "$VISITS_CSV" == customer_name,* ]]
  echo "  ✓ visit CSV export"
else
  echo "$HTML" | grep -q 'Alex Rivera'
  echo "  ✓ sample member row"
  echo "$HTML" | grep -q 'Collected'
  echo "  ✓ summary stats"
fi

echo "All checks passed for ${CLUBDUE_URL}"
