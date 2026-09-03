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
  DEMO_COOKIE_JAR="$(mktemp)"
  SECOND_DEMO_COOKIE_JAR="$(mktemp)"
  SECOND_DEMO_REPLAY_JAR="$(mktemp)"
  DEMO_IMPORT_FIXTURE="${SCRIPT_DIR}/../internal/app/static/landscaping-demo-import.csv"
  trap 'rm -f "$COOKIE_JAR" "$DEMO_COOKIE_JAR" "$SECOND_DEMO_COOKIE_JAR" "$SECOND_DEMO_REPLAY_JAR"' EXIT
  [ -r "$DEMO_IMPORT_FIXTURE" ]
  curl -fsS "${CLUBDUE_URL}/rollback-safetyz" | grep -qx demo-sandbox-v1
  echo "  ✓ rollback safety marker"
  LOGIN_PAGE="$(curl -fsS -c "$COOKIE_JAR" "${CLUBDUE_URL}/login")"
  CSRF_TOKEN="$(printf '%s' "$LOGIN_PAGE" | sed -n 's/.*name="csrf_token" value="\([^"]*\)".*/\1/p' | head -1)"
  [ -n "$CSRF_TOKEN" ]
  [[ "$LOGIN_PAGE" == *'Explore a landscaping demo'* ]]
  SIGNUP_PAGE="$(curl -fsS "${CLUBDUE_URL}/signup")"
  [[ "$SIGNUP_PAGE" == *'invitation'* ]]
  DEV_SEED_STATUS="$(curl -sS -b "$COOKIE_JAR" -o /dev/null -w '%{http_code}' -X POST \
    --data-urlencode "csrf_token=$CSRF_TOKEN" "${CLUBDUE_URL}/dev/seed")"
  [ "$DEV_SEED_STATUS" = "404" ]
  LEGACY_LOGIN_STATUS="$(curl -fsS -b "$COOKIE_JAR" -c "$COOKIE_JAR" -o /dev/null -w '%{http_code}' \
    --data-urlencode "csrf_token=$CSRF_TOKEN" \
    --data-urlencode 'email=owner@demo.local' \
    --data-urlencode 'password=demo1234' "${CLUBDUE_URL}/login")"
  [ "$LEGACY_LOGIN_STATUS" = "200" ]
  echo "  ✓ staging signup/dev routes closed and legacy shared login retired"

  DEMO_PAGE="$(curl -fsS -c "$DEMO_COOKIE_JAR" "${CLUBDUE_URL}/demo")"
  DEMO_CSRF_TOKEN="$(printf '%s' "$DEMO_PAGE" | sed -n 's/.*name="csrf_token" value="\([^"]*\)".*/\1/p' | head -1)"
  [ -n "$DEMO_CSRF_TOKEN" ]
  DEMO_STATUS="$(curl -fsS --max-time 20 -b "$DEMO_COOKIE_JAR" -c "$DEMO_COOKIE_JAR" -o /dev/null -w '%{http_code}' \
    --data-urlencode "csrf_token=$DEMO_CSRF_TOKEN" "${CLUBDUE_URL}/demo/session")"
  [ "$DEMO_STATUS" = "303" ]
  DEMO_HTML="$(curl -fsS -b "$DEMO_COOKIE_JAR" "${CLUBDUE_URL}/dashboard")"
  [[ "$DEMO_HTML" == *'Evergreen Lawn &amp; Landscape'* ]]
  [[ "$DEMO_HTML" == *'Fictional public demo'* ]]
  [[ "$DEMO_HTML" == *'$4715.33'* ]]
  [[ "$DEMO_HTML" == *'2 lapsed MTD'* ]]
  [[ "$DEMO_HTML" == *'4 in the next 90 days'* ]]
  DEMO_SAMPLE_CSV="$(curl -fsS "${CLUBDUE_URL}/static/landscaping-demo-import.csv")"
  [[ "$DEMO_SAMPLE_CSV" == external_id,customer_name,* ]]
  [[ "$DEMO_SAMPLE_CSV" == *'Jordan Green'* ]]
  HTML="$DEMO_HTML"

  DEMO_BASE_CUSTOMERS="$(curl -fsS -b "$DEMO_COOKIE_JAR" "${CLUBDUE_URL}/customers")"
  [[ "$DEMO_BASE_CUSTOMERS" == *'Maya Thompson'* ]]
  [[ "$DEMO_BASE_CUSTOMERS" == *'Willowmere Commons HOA'* ]]
  [[ "$DEMO_BASE_CUSTOMERS" == *'Buckeye Key Property Management'* ]]
  DEMO_BASE_CUSTOMER_COUNT="$(printf '%s' "$DEMO_BASE_CUSTOMERS" | grep -o 'class="secondary-btn">Edit</a>' | wc -l | tr -d ' ')"
  [ "$DEMO_BASE_CUSTOMER_COUNT" = "30" ]

  DEMO_WORK_BEFORE="$(curl -fsS -b "$DEMO_COOKIE_JAR" "${CLUBDUE_URL}/visits?filter=due")"
  DEMO_DUE_COUNT="$(printf '%s' "$DEMO_WORK_BEFORE" | grep -o 'name="occurrence_id"' | wc -l | tr -d ' ')"
  [ "$DEMO_DUE_COUNT" -le 60 ]
  [[ "$DEMO_WORK_BEFORE" == *'Weekly mow, trim, edge &amp; blow'* ]]
  [[ "$DEMO_WORK_BEFORE" == *'Seasonal Cleanup Plan'* ]]
  DEMO_ALL_WORK="$(curl -fsS -b "$DEMO_COOKIE_JAR" "${CLUBDUE_URL}/visits?filter=all")"
  [[ "$DEMO_ALL_WORK" == *'>Paused</span>'* ]]
  [[ "$DEMO_ALL_WORK" == *'>Plan inactive</span>'* ]]
  [[ "$DEMO_ALL_WORK" == *'>Completed</span>'* ]]
  [[ "$DEMO_ALL_WORK" == *'>Skipped</span>'* ]]
  [ "${#DEMO_ALL_WORK}" -le 524288 ]
  DEMO_OCCURRENCE_ID="$(printf '%s' "$DEMO_WORK_BEFORE" | sed -n 's/.*name="occurrence_id" value="\([^"]*\)".*/\1/p' | head -1)"
  [ -n "$DEMO_OCCURRENCE_ID" ]
  DEMO_WORK_STATUS="$(curl -fsS -b "$DEMO_COOKIE_JAR" -c "$DEMO_COOKIE_JAR" -o /dev/null -w '%{http_code}' -X POST \
    --data-urlencode "csrf_token=$DEMO_CSRF_TOKEN" \
    --data-urlencode 'filter=due' \
    --data-urlencode "occurrence_id=$DEMO_OCCURRENCE_ID" \
    --data-urlencode 'action=completed' \
    "${CLUBDUE_URL}/visits?filter=due")"
  [ "$DEMO_WORK_STATUS" = "303" ]
  DEMO_WORK_AFTER="$(curl -fsS -b "$DEMO_COOKIE_JAR" "${CLUBDUE_URL}/visits?filter=due")"
  DEMO_DUE_AFTER="$(printf '%s' "$DEMO_WORK_AFTER" | grep -o 'name="occurrence_id"' | wc -l | tr -d ' ')"
  [ "$DEMO_DUE_AFTER" -lt "$DEMO_DUE_COUNT" ]
  DEMO_CUSTOMER_STATUS="$(curl -fsS -b "$DEMO_COOKIE_JAR" -c "$DEMO_COOKIE_JAR" -o /dev/null -w '%{http_code}' -X POST \
    --data-urlencode "csrf_token=$DEMO_CSRF_TOKEN" \
    --data-urlencode 'name=Staging Smoke Garden Client' \
    --data-urlencode 'email=staging-smoke@example.test' \
    --data-urlencode 'phone=555-0199' "${CLUBDUE_URL}/customers")"
  [ "$DEMO_CUSTOMER_STATUS" = "303" ]
  DEMO_CUSTOMERS="$(curl -fsS -b "$DEMO_COOKIE_JAR" "${CLUBDUE_URL}/customers")"
  [[ "$DEMO_CUSTOMERS" == *'Staging Smoke Garden Client'* ]]
  [[ "$DEMO_CUSTOMERS" != *'Jordan Green'* ]]

  DEMO_IMPORT_PREVIEW="$(curl -fsS -b "$DEMO_COOKIE_JAR" -c "$DEMO_COOKIE_JAR" \
    -F "csrf_token=$DEMO_CSRF_TOKEN" \
    -F 'step=dry_run' \
    -F "csv=@${DEMO_IMPORT_FIXTURE};type=text/csv" "${CLUBDUE_URL}/import")"
  [[ "$DEMO_IMPORT_PREVIEW" == *'Import preview'* ]]
  [[ "$DEMO_IMPORT_PREVIEW" == *'Nothing has been saved yet'* ]]
  [[ "$DEMO_IMPORT_PREVIEW" == *'Jordan Green'* ]]
  [[ "$DEMO_IMPORT_PREVIEW" != *'name="csv_data"'* ]]
  DEMO_PREVIEW_TOKEN="$(printf '%s' "$DEMO_IMPORT_PREVIEW" | sed -n 's/.*name="preview_token" value="\([^"]*\)".*/\1/p' | head -1)"
  [ -n "$DEMO_PREVIEW_TOKEN" ]
  DEMO_CUSTOMERS_AFTER_PREVIEW="$(curl -fsS -b "$DEMO_COOKIE_JAR" "${CLUBDUE_URL}/customers")"
  [[ "$DEMO_CUSTOMERS_AFTER_PREVIEW" != *'Jordan Green'* ]]

  DEMO_IMPORT_COMMIT="$(curl -fsS -b "$DEMO_COOKIE_JAR" -c "$DEMO_COOKIE_JAR" -X POST \
    --data-urlencode "csrf_token=$DEMO_CSRF_TOKEN" \
    --data-urlencode 'step=commit' \
    --data-urlencode "preview_token=$DEMO_PREVIEW_TOKEN" "${CLUBDUE_URL}/import")"
  [[ "$DEMO_IMPORT_COMMIT" == *'Import complete'* ]]
  [[ "$DEMO_IMPORT_COMMIT" == *'3 new customers'* ]]
  DEMO_CUSTOMERS_AFTER_IMPORT="$(curl -fsS -b "$DEMO_COOKIE_JAR" "${CLUBDUE_URL}/customers")"
  [[ "$DEMO_CUSTOMERS_AFTER_IMPORT" == *'Jordan Green'* ]]

  DEMO_NON_IMPORT_MULTIPART_STATUS="$(curl -sS -b "$DEMO_COOKIE_JAR" -o /dev/null -w '%{http_code}' \
    -F "csrf_token=$DEMO_CSRF_TOKEN" \
    -F "csv=@${DEMO_IMPORT_FIXTURE};type=text/csv" "${CLUBDUE_URL}/customers")"
  [ "$DEMO_NON_IMPORT_MULTIPART_STATUS" = "403" ]

  SECOND_DEMO_PAGE="$(curl -fsS -c "$SECOND_DEMO_COOKIE_JAR" "${CLUBDUE_URL}/demo")"
  SECOND_DEMO_CSRF_TOKEN="$(printf '%s' "$SECOND_DEMO_PAGE" | sed -n 's/.*name="csrf_token" value="\([^"]*\)".*/\1/p' | head -1)"
  [ -n "$SECOND_DEMO_CSRF_TOKEN" ]
  SECOND_DEMO_STATUS="$(curl -fsS --max-time 20 -b "$SECOND_DEMO_COOKIE_JAR" -c "$SECOND_DEMO_COOKIE_JAR" -o /dev/null -w '%{http_code}' \
    --data-urlencode "csrf_token=$SECOND_DEMO_CSRF_TOKEN" "${CLUBDUE_URL}/demo/session")"
  [ "$SECOND_DEMO_STATUS" = "303" ]
  SECOND_DEMO_WORK="$(curl -fsS -b "$SECOND_DEMO_COOKIE_JAR" "${CLUBDUE_URL}/visits?filter=due")"
  SECOND_DEMO_DUE_COUNT="$(printf '%s' "$SECOND_DEMO_WORK" | grep -o 'name="occurrence_id"' | wc -l | tr -d ' ')"
  [ "$SECOND_DEMO_DUE_COUNT" = "$DEMO_DUE_COUNT" ]
  SECOND_DEMO_CUSTOMERS="$(curl -fsS -b "$SECOND_DEMO_COOKIE_JAR" "${CLUBDUE_URL}/customers")"
  [[ "$SECOND_DEMO_CUSTOMERS" != *'Staging Smoke Garden Client'* ]]
  [[ "$SECOND_DEMO_CUSTOMERS" != *'Jordan Green'* ]]
  cp "$SECOND_DEMO_COOKIE_JAR" "$SECOND_DEMO_REPLAY_JAR"
  SECOND_DEMO_LOGOUT_STATUS="$(curl -fsS -b "$SECOND_DEMO_COOKIE_JAR" -c "$SECOND_DEMO_COOKIE_JAR" -o /dev/null -w '%{http_code}' -X POST \
    --data-urlencode "csrf_token=$SECOND_DEMO_CSRF_TOKEN" "${CLUBDUE_URL}/logout")"
  [ "$SECOND_DEMO_LOGOUT_STATUS" = "303" ]
  SECOND_DEMO_DASHBOARD_STATUS="$(curl -fsS -b "$SECOND_DEMO_REPLAY_JAR" -o /dev/null -w '%{http_code}' "${CLUBDUE_URL}/dashboard")"
  [ "$SECOND_DEMO_DASHBOARD_STATUS" = "303" ]
  echo "  ✓ isolated demo writes, protected CSV preview/commit, non-import upload denial, and tenant separation"
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
  DEMO_STATUS="$(curl -sS -o /dev/null -w '%{http_code}' "${CLUBDUE_URL}/demo")"
  [ "$DEMO_STATUS" = "404" ]
  echo "  ✓ public demo disabled in production"
fi
echo "$HTML" | grep -q 'ClubDue'
echo "  ✓ dashboard title"

if [ "$TARGET" = "staging" ]; then
  echo "$HTML" | grep -q 'Active agreements'
  echo "  ✓ dashboard metrics"
  AGREEMENTS_HTML="$(curl -fsS -b "$DEMO_COOKIE_JAR" "${CLUBDUE_URL}/agreements")"
  [[ "$AGREEMENTS_HTML" == *'Maya Thompson'* ]]
  [[ "$AGREEMENTS_HTML" == *'Commercial Grounds Care'* ]]
  [[ "$AGREEMENTS_HTML" == *'Pending renewal'* ]]
  [[ "$AGREEMENTS_HTML" == *'Past due'* ]]
  DEMO_AGREEMENT_COUNT="$(printf '%s' "$AGREEMENTS_HTML" | grep -o 'Open agreement' | wc -l | tr -d ' ')"
  [ "$DEMO_AGREEMENT_COUNT" = "29" ]
  echo "  ✓ fictional landscaping agreement book"
  RENEWALS_HTML="$(curl -fsS -b "$DEMO_COOKIE_JAR" "${CLUBDUE_URL}/renewals")"
  [[ "$RENEWALS_HTML" == *'Renewal queue'* ]]
  DEMO_RENEWAL_COUNT="$(printf '%s' "$RENEWALS_HTML" | grep -o 'name="task_id"' | wc -l | tr -d ' ')"
  [ "$DEMO_RENEWAL_COUNT" -le 15 ]
  echo "  ✓ renewal queue"
  RENEWAL_TASK_ID="$(printf '%s' "$RENEWALS_HTML" | sed -n 's/.*name="task_id" value="\([^"]*\)".*/\1/p' | head -1)"
  [ -n "$RENEWAL_TASK_ID" ]
  RENEWAL_STATUS="$(curl -fsS -b "$DEMO_COOKIE_JAR" -c "$DEMO_COOKIE_JAR" -o /dev/null -w '%{http_code}' -X POST \
    --data-urlencode "csrf_token=$DEMO_CSRF_TOKEN" \
    --data-urlencode "task_id=$RENEWAL_TASK_ID" \
    --data-urlencode 'status=contacted' \
    --data-urlencode 'notes=staging smoke verification' \
    "${CLUBDUE_URL}/renewals")"
  [ "$RENEWAL_STATUS" = "303" ]
  RENEWALS_AFTER_WRITE="$(curl -fsS -b "$DEMO_COOKIE_JAR" "${CLUBDUE_URL}/renewals")"
  [[ "$RENEWALS_AFTER_WRITE" == *'staging smoke verification'* ]]
  echo "  ✓ persistent renewal outcome and notes mutation"
  VISITS_CSV="$(curl -fsS -b "$DEMO_COOKIE_JAR" "${CLUBDUE_URL}/visits/export.csv")"
  [[ "$VISITS_CSV" == customer_name,* ]]
  echo "  ✓ service-work CSV export"
else
  echo "$HTML" | grep -q 'Active agreements'
  echo "  ✓ dashboard metrics"
fi

echo "All checks passed for ${CLUBDUE_URL}"
