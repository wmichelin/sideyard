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
  : "${CLUBDUE_SMOKE_EMAIL:?Set CLUBDUE_SMOKE_EMAIL from a GitHub environment secret}"
  : "${CLUBDUE_SMOKE_PASSWORD:?Set CLUBDUE_SMOKE_PASSWORD from a GitHub environment secret}"
  COOKIE_JAR="$(mktemp)"
  trap 'rm -f "$COOKIE_JAR"' EXIT
  LOGIN_PAGE="$(curl -fsS -c "$COOKIE_JAR" "${CLUBDUE_URL}/login")"
  CSRF_TOKEN="$(printf '%s' "$LOGIN_PAGE" | sed -n 's/.*name="csrf_token" value="\([^"]*\)".*/\1/p' | head -1)"
  [ -n "$CSRF_TOKEN" ]
  LOGIN_STATUS="$(curl -fsS -b "$COOKIE_JAR" -c "$COOKIE_JAR" -o /dev/null -w '%{http_code}' \
    --data-urlencode "csrf_token=$CSRF_TOKEN" \
    --data-urlencode "email=$CLUBDUE_SMOKE_EMAIL" \
    --data-urlencode "password=$CLUBDUE_SMOKE_PASSWORD" "${CLUBDUE_URL}/login")"
  [ "$LOGIN_STATUS" = "303" ]
  echo "  ✓ production login"
  HTML="$(curl -fsS -b "$COOKIE_JAR" "${CLUBDUE_URL}/dashboard")"
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
  RENEWAL_TASK_ID="$(printf '%s' "$RENEWALS_HTML" | sed -n 's/.*name="task_id" value="\([^"]*\)".*/\1/p' | head -1)"
  [ -n "$RENEWAL_TASK_ID" ]
  RENEWAL_STATUS="$(curl -fsS -b "$COOKIE_JAR" -c "$COOKIE_JAR" -o /dev/null -w '%{http_code}' -X POST \
    --data-urlencode "csrf_token=$CSRF_TOKEN" \
    --data-urlencode "task_id=$RENEWAL_TASK_ID" \
    --data-urlencode 'status=contacted' \
    --data-urlencode 'notes=staging smoke verification' \
    "${CLUBDUE_URL}/renewals")"
  [ "$RENEWAL_STATUS" = "303" ]
  echo "  ✓ renewal outcome mutation"
  VISITS_HTML="$(curl -fsS -b "$COOKIE_JAR" "${CLUBDUE_URL}/visits?filter=due")"
  [[ "$VISITS_HTML" == *'Visit due-list'* ]]
  VISIT_ID="$(printf '%s' "$VISITS_HTML" | sed -n 's/.*name="visit_id" value="\([^"]*\)".*/\1/p' | head -1)"
  [ -n "$VISIT_ID" ]
  VISIT_STATUS="$(curl -fsS -b "$COOKIE_JAR" -c "$COOKIE_JAR" -o /dev/null -w '%{http_code}' -X POST \
    --data-urlencode "csrf_token=$CSRF_TOKEN" \
    --data-urlencode 'filter=due' \
    --data-urlencode "visit_id=$VISIT_ID" \
    --data-urlencode 'action=scheduled' \
    "${CLUBDUE_URL}/visits?filter=due")"
  [ "$VISIT_STATUS" = "303" ]
  echo "  ✓ visit handoff mutation"
  VISITS_CSV="$(curl -fsS -b "$COOKIE_JAR" "${CLUBDUE_URL}/visits/export.csv")"
  [[ "$VISITS_CSV" == customer_name,* ]]
  echo "  ✓ visit CSV export"
else
  echo "$HTML" | grep -q 'Active agreements'
  echo "  ✓ dashboard metrics"
fi

echo "All checks passed for ${CLUBDUE_URL}"
