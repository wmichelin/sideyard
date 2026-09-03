#!/usr/bin/env bash
# Smoke-test a deployed ClubDue environment (staging or production).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/env.sh
source "${SCRIPT_DIR}/env.sh"

TARGET="${1:-staging}"
clubdue_env "$TARGET"
if [ "$CLUBDUE_ENV" = staging ]; then
  PUBLIC_DEMO_EXPECTATION=enabled
else
  : "${CLUBDUE_SMOKE_PUBLIC_DEMO:?Set CLUBDUE_SMOKE_PUBLIC_DEMO to enabled, disabled, or either}"
  PUBLIC_DEMO_EXPECTATION="$CLUBDUE_SMOKE_PUBLIC_DEMO"
  case "$PUBLIC_DEMO_EXPECTATION" in enabled|disabled|either) ;; *) echo "CLUBDUE_SMOKE_PUBLIC_DEMO must be enabled, disabled, or either" >&2; exit 1 ;; esac
fi

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT
LOGIN_COOKIE_JAR="$WORK_DIR/login.cookies"
LEGACY_COOKIE_JAR="$WORK_DIR/legacy.cookies"
OWNER_COOKIE_JAR="$WORK_DIR/owner.cookies"
DEMO_COOKIE_JAR="$WORK_DIR/demo.cookies"
SECOND_DEMO_COOKIE_JAR="$WORK_DIR/second-demo.cookies"
SECOND_DEMO_REPLAY_JAR="$WORK_DIR/second-demo-replay.cookies"
DEMO_IMPORT_FIXTURE="${SCRIPT_DIR}/../internal/app/static/landscaping-demo-import.csv"
[ -r "$DEMO_IMPORT_FIXTURE" ]

csrf_from_file() {
  sed -n 's/.*name="csrf_token" value="\([^"]*\)".*/\1/p' "$1" | head -1
}

echo "Smoke-testing ${CLUBDUE_ENV} at ${CLUBDUE_URL} ..."

curl -fsS "${CLUBDUE_URL}/healthz" | grep -qx ok
echo "  ✓ /healthz"
curl -fsS "${CLUBDUE_URL}/livez" | grep -qx ok
echo "  ✓ /livez"
curl -fsS "${CLUBDUE_URL}/readyz" | grep -qx ok
echo "  ✓ /readyz"
curl -fsS "${CLUBDUE_URL}/rollback-safetyz" | grep -qx demo-sandbox-v1
echo "  ✓ rollback safety marker"

LOGIN_PAGE_FILE="$WORK_DIR/login.html"
curl -fsS -c "$LOGIN_COOKIE_JAR" -o "$LOGIN_PAGE_FILE" "${CLUBDUE_URL}/login"
CSRF_TOKEN="$(csrf_from_file "$LOGIN_PAGE_FILE")"
[ -n "$CSRF_TOKEN" ]
SIGNUP_PAGE="$(curl -fsS "${CLUBDUE_URL}/signup")"
[[ "$SIGNUP_PAGE" == *'invitation'* ]]
DEV_SEED_STATUS="$(curl -sS -b "$LOGIN_COOKIE_JAR" -o /dev/null -w '%{http_code}' -X POST \
  --data-urlencode "csrf_token=$CSRF_TOKEN" "${CLUBDUE_URL}/dev/seed")"
[ "$DEV_SEED_STATUS" = "404" ]

LEGACY_LOGIN_FILE="$WORK_DIR/legacy-login.html"
curl -fsS -c "$LEGACY_COOKIE_JAR" -o "$LEGACY_LOGIN_FILE" "${CLUBDUE_URL}/login"
LEGACY_CSRF_TOKEN="$(csrf_from_file "$LEGACY_LOGIN_FILE")"
[ -n "$LEGACY_CSRF_TOKEN" ]
LEGACY_LOGIN_STATUS="$(curl -sS -b "$LEGACY_COOKIE_JAR" -c "$LEGACY_COOKIE_JAR" -o "$WORK_DIR/legacy-result.html" -w '%{http_code}' \
  --data-urlencode "csrf_token=$LEGACY_CSRF_TOKEN" \
  --data-urlencode 'email=owner@demo.local' \
  --data-urlencode 'password=demo1234' "${CLUBDUE_URL}/login")"
[ "$LEGACY_LOGIN_STATUS" = "200" ]
if grep -q $'\tclubdue_session\t' "$LEGACY_COOKIE_JAR"; then
  echo "legacy shared demo credentials unexpectedly created a session" >&2
  exit 1
fi
LEGACY_DASHBOARD_STATUS="$(curl -sS -b "$LEGACY_COOKIE_JAR" -o /dev/null -w '%{http_code}' "${CLUBDUE_URL}/dashboard")"
[ "$LEGACY_DASHBOARD_STATUS" = "303" ]
echo "  ✓ signup/dev routes closed and legacy shared login retired"

if [ "$CLUBDUE_ENV" = "production" ]; then
  : "${CLUBDUE_SMOKE_EMAIL:?Set CLUBDUE_SMOKE_EMAIL from a GitHub environment secret}"
  : "${CLUBDUE_SMOKE_PASSWORD:?Set CLUBDUE_SMOKE_PASSWORD from a GitHub environment secret}"
  OWNER_LOGIN_FILE="$WORK_DIR/owner-login.html"
  curl -fsS -c "$OWNER_COOKIE_JAR" -o "$OWNER_LOGIN_FILE" "${CLUBDUE_URL}/login"
  OWNER_CSRF_TOKEN="$(csrf_from_file "$OWNER_LOGIN_FILE")"
  [ -n "$OWNER_CSRF_TOKEN" ]
  OWNER_LOGIN_STATUS="$(curl -sS -b "$OWNER_COOKIE_JAR" -c "$OWNER_COOKIE_JAR" -o /dev/null -w '%{http_code}' \
    --data-urlencode "csrf_token=$OWNER_CSRF_TOKEN" \
    --data-urlencode "email=$CLUBDUE_SMOKE_EMAIL" \
    --data-urlencode "password=$CLUBDUE_SMOKE_PASSWORD" "${CLUBDUE_URL}/login")"
  [ "$OWNER_LOGIN_STATUS" = "303" ]
  OWNER_HTML="$(curl -fsS -b "$OWNER_COOKIE_JAR" "${CLUBDUE_URL}/dashboard")"
  [[ "$OWNER_HTML" == *'Dashboard — ClubDue'* ]]
  [[ "$OWNER_HTML" == *'Active agreements'* ]]
  [[ "$OWNER_HTML" != *'Fictional public demo'* ]]
  echo "  ✓ production owner login remains separate"
fi

DEMO_ENTRY_FILE="$WORK_DIR/demo-entry.html"
DEMO_ENTRY_HEADERS="$WORK_DIR/demo-entry.headers"
DEMO_ENTRY_STATUS="$(curl -sS -D "$DEMO_ENTRY_HEADERS" -c "$DEMO_COOKIE_JAR" -o "$DEMO_ENTRY_FILE" -w '%{http_code}' "${CLUBDUE_URL}/demo")"
case "$PUBLIC_DEMO_EXPECTATION:$DEMO_ENTRY_STATUS" in
  enabled:200|either:200) DEMO_ENABLED=1 ;;
  disabled:404|either:404) DEMO_ENABLED=0 ;;
  *) echo "public demo status ${DEMO_ENTRY_STATUS} did not match ${PUBLIC_DEMO_EXPECTATION}" >&2; exit 1 ;;
esac

if [ "$DEMO_ENABLED" = 0 ]; then
  if grep -q 'Explore a landscaping demo' "$LOGIN_PAGE_FILE"; then
    echo "login exposed a public demo CTA while demo routes were disabled" >&2
    exit 1
  fi
  if [ "$CLUBDUE_ENV" = production ]; then
    OWNER_HTML_AFTER="$(curl -fsS -b "$OWNER_COOKIE_JAR" "${CLUBDUE_URL}/dashboard")"
    [[ "$OWNER_HTML_AFTER" == *'Dashboard — ClubDue'* ]]
    [[ "$OWNER_HTML_AFTER" != *'Fictional public demo'* ]]
  fi
  echo "  ✓ public demo explicitly disabled"
  echo "All checks passed for ${CLUBDUE_URL}"
  exit 0
fi

grep -q 'Explore a landscaping demo' "$LOGIN_PAGE_FILE"
grep -q 'action="/demo/session"' "$DEMO_ENTRY_FILE"
grep -Eiq '^Cache-Control:.*no-store' "$DEMO_ENTRY_HEADERS"
grep -Eiq '^X-Robots-Tag:.*noindex' "$DEMO_ENTRY_HEADERS"
if grep -Eiq '^Set-Cookie:[[:space:]]*clubdue_session=' "$DEMO_ENTRY_HEADERS"; then
  echo "demo entry created an authenticated session before consent" >&2
  exit 1
fi
DEMO_CSRF_TOKEN="$(csrf_from_file "$DEMO_ENTRY_FILE")"
[ -n "$DEMO_CSRF_TOKEN" ]
DEMO_SESSION_HEADERS="$WORK_DIR/demo-session.headers"
DEMO_STATUS="$(curl -sS --max-time 20 -D "$DEMO_SESSION_HEADERS" -b "$DEMO_COOKIE_JAR" -c "$DEMO_COOKIE_JAR" -o /dev/null -w '%{http_code}' \
  --data-urlencode "csrf_token=$DEMO_CSRF_TOKEN" "${CLUBDUE_URL}/demo/session")"
[ "$DEMO_STATUS" = "303" ]
grep -Eiq '^Location:[[:space:]]*/dashboard' "$DEMO_SESSION_HEADERS"
grep -Eiq '^Set-Cookie:[[:space:]]*clubdue_session=.*Secure' "$DEMO_SESSION_HEADERS"
grep -Eiq '^Set-Cookie:[[:space:]]*clubdue_session=.*HttpOnly' "$DEMO_SESSION_HEADERS"
grep -Eiq '^Set-Cookie:[[:space:]]*clubdue_session=.*SameSite=Lax' "$DEMO_SESSION_HEADERS"

DEMO_HTML="$(curl -fsS -b "$DEMO_COOKIE_JAR" "${CLUBDUE_URL}/dashboard")"
[[ "$DEMO_HTML" == *'Dashboard — ClubDue'* ]]
[[ "$DEMO_HTML" == *'Evergreen Lawn &amp; Landscape'* ]]
[[ "$DEMO_HTML" == *'Fictional public demo'* ]]
[[ "$DEMO_HTML" == *'$4715.33'* ]]
[[ "$DEMO_HTML" == *'2 lapsed MTD'* ]]
[[ "$DEMO_HTML" == *'4 in the next 90 days'* ]]
DEMO_SAMPLE_CSV="$(curl -fsS "${CLUBDUE_URL}/static/landscaping-demo-import.csv")"
[[ "$DEMO_SAMPLE_CSV" == external_id,customer_name,* ]]
[[ "$DEMO_SAMPLE_CSV" == *'Jordan Green'* ]]

DEMO_BASE_CUSTOMERS="$(curl -fsS -b "$DEMO_COOKIE_JAR" "${CLUBDUE_URL}/customers")"
[[ "$DEMO_BASE_CUSTOMERS" == *'Maya Thompson'* ]]
[[ "$DEMO_BASE_CUSTOMERS" == *'Willowmere Commons HOA'* ]]
[[ "$DEMO_BASE_CUSTOMERS" == *'Buckeye Key Property Management'* ]]
DEMO_BASE_CUSTOMER_COUNT="$(printf '%s' "$DEMO_BASE_CUSTOMERS" | grep -o 'class="secondary-btn">Edit</a>' | wc -l | tr -d ' ')"
[ "$DEMO_BASE_CUSTOMER_COUNT" = "30" ]

DEMO_WORK_BEFORE="$(curl -fsS -b "$DEMO_COOKIE_JAR" "${CLUBDUE_URL}/visits?filter=due")"
DEMO_DUE_COUNT="$(printf '%s' "$DEMO_WORK_BEFORE" | grep -o 'name="occurrence_id"' | wc -l | tr -d ' ')"
[ "$DEMO_DUE_COUNT" -gt 0 ]
[ "$DEMO_DUE_COUNT" -le 60 ]
[[ "$DEMO_WORK_BEFORE" == *'Weekly mow, trim, edge &amp; blow'* ]]
[[ "$DEMO_WORK_BEFORE" == *'Seasonal Cleanup Plan'* ]]
DEMO_ALL_WORK="$(curl -fsS -b "$DEMO_COOKIE_JAR" "${CLUBDUE_URL}/visits?filter=all")"
[[ "$DEMO_ALL_WORK" == *'>Paused</span>'* ]]
[[ "$DEMO_ALL_WORK" == *'>Plan inactive</span>'* ]]
[[ "$DEMO_ALL_WORK" == *'>Completed</span>'* ]]
[[ "$DEMO_ALL_WORK" == *'>Skipped</span>'* ]]
[ "${#DEMO_ALL_WORK}" -le 524288 ]

if [ "$CLUBDUE_ENV" = "production" ]; then
  DEMO_SMOKE_NAME='Production Demo Smoke Client'
  DEMO_SMOKE_EMAIL='production-demo-smoke@example.test'
else
  DEMO_SMOKE_NAME='Staging Smoke Garden Client'
  DEMO_SMOKE_EMAIL='staging-smoke@example.test'
fi
DEMO_CUSTOMER_STATUS="$(curl -sS -b "$DEMO_COOKIE_JAR" -c "$DEMO_COOKIE_JAR" -o /dev/null -w '%{http_code}' -X POST \
  --data-urlencode "csrf_token=$DEMO_CSRF_TOKEN" \
  --data-urlencode "name=$DEMO_SMOKE_NAME" \
  --data-urlencode "email=$DEMO_SMOKE_EMAIL" \
  --data-urlencode 'phone=555-0199' "${CLUBDUE_URL}/customers")"
[ "$DEMO_CUSTOMER_STATUS" = "303" ]
DEMO_CUSTOMERS_AFTER_WRITE_FILE="$WORK_DIR/demo-customers-after-write.html"
curl -fsS -b "$DEMO_COOKIE_JAR" -o "$DEMO_CUSTOMERS_AFTER_WRITE_FILE" "${CLUBDUE_URL}/customers"
grep -q "$DEMO_SMOKE_NAME" "$DEMO_CUSTOMERS_AFTER_WRITE_FILE"
DEMO_CUSTOMER_ID="$(grep -o 'href="/customers/[^"]*" class="secondary-btn"' "$DEMO_CUSTOMERS_AFTER_WRITE_FILE" | head -1 | sed 's#href="/customers/\([^"]*\)".*#\1#')"
[ -n "$DEMO_CUSTOMER_ID" ]

if [ "$CLUBDUE_ENV" = "staging" ]; then
  DEMO_OCCURRENCE_ID="$(printf '%s' "$DEMO_WORK_BEFORE" | sed -n 's/.*name="occurrence_id" value="\([^"]*\)".*/\1/p' | head -1)"
  [ -n "$DEMO_OCCURRENCE_ID" ]
  DEMO_WORK_STATUS="$(curl -sS -b "$DEMO_COOKIE_JAR" -c "$DEMO_COOKIE_JAR" -o /dev/null -w '%{http_code}' -X POST \
    --data-urlencode "csrf_token=$DEMO_CSRF_TOKEN" \
    --data-urlencode 'filter=due' \
    --data-urlencode "occurrence_id=$DEMO_OCCURRENCE_ID" \
    --data-urlencode 'action=completed' \
    "${CLUBDUE_URL}/visits?filter=due")"
  [ "$DEMO_WORK_STATUS" = "303" ]
  DEMO_WORK_AFTER="$(curl -fsS -b "$DEMO_COOKIE_JAR" "${CLUBDUE_URL}/visits?filter=due")"
  DEMO_DUE_AFTER="$(printf '%s' "$DEMO_WORK_AFTER" | grep -o 'name="occurrence_id"' | wc -l | tr -d ' ')"
  [ "$DEMO_DUE_AFTER" -lt "$DEMO_DUE_COUNT" ]

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
fi

SECOND_DEMO_ENTRY_FILE="$WORK_DIR/second-demo-entry.html"
curl -fsS -c "$SECOND_DEMO_COOKIE_JAR" -o "$SECOND_DEMO_ENTRY_FILE" "${CLUBDUE_URL}/demo"
SECOND_DEMO_CSRF_TOKEN="$(csrf_from_file "$SECOND_DEMO_ENTRY_FILE")"
[ -n "$SECOND_DEMO_CSRF_TOKEN" ]
SECOND_DEMO_STATUS="$(curl -sS --max-time 20 -b "$SECOND_DEMO_COOKIE_JAR" -c "$SECOND_DEMO_COOKIE_JAR" -o /dev/null -w '%{http_code}' \
  --data-urlencode "csrf_token=$SECOND_DEMO_CSRF_TOKEN" "${CLUBDUE_URL}/demo/session")"
[ "$SECOND_DEMO_STATUS" = "303" ]
SECOND_DEMO_WORK="$(curl -fsS -b "$SECOND_DEMO_COOKIE_JAR" "${CLUBDUE_URL}/visits?filter=due")"
SECOND_DEMO_DUE_COUNT="$(printf '%s' "$SECOND_DEMO_WORK" | grep -o 'name="occurrence_id"' | wc -l | tr -d ' ')"
[ "$SECOND_DEMO_DUE_COUNT" = "$DEMO_DUE_COUNT" ]
SECOND_DEMO_CUSTOMERS="$(curl -fsS -b "$SECOND_DEMO_COOKIE_JAR" "${CLUBDUE_URL}/customers")"
[[ "$SECOND_DEMO_CUSTOMERS" != *"$DEMO_SMOKE_NAME"* ]]
[[ "$SECOND_DEMO_CUSTOMERS" != *'Jordan Green'* ]]
FOREIGN_CUSTOMER_STATUS="$(curl -sS -b "$SECOND_DEMO_COOKIE_JAR" -o /dev/null -w '%{http_code}' "${CLUBDUE_URL}/customers/${DEMO_CUSTOMER_ID}")"
[ "$FOREIGN_CUSTOMER_STATUS" = "404" ]
cp "$SECOND_DEMO_COOKIE_JAR" "$SECOND_DEMO_REPLAY_JAR"
SECOND_DEMO_LOGOUT_STATUS="$(curl -sS -b "$SECOND_DEMO_COOKIE_JAR" -c "$SECOND_DEMO_COOKIE_JAR" -o /dev/null -w '%{http_code}' -X POST \
  --data-urlencode "csrf_token=$SECOND_DEMO_CSRF_TOKEN" "${CLUBDUE_URL}/logout")"
[ "$SECOND_DEMO_LOGOUT_STATUS" = "303" ]
SECOND_DEMO_DASHBOARD_STATUS="$(curl -sS -b "$SECOND_DEMO_REPLAY_JAR" -o /dev/null -w '%{http_code}' "${CLUBDUE_URL}/dashboard")"
[ "$SECOND_DEMO_DASHBOARD_STATUS" = "303" ]
echo "  ✓ isolated writable demo tenants and expired-session rejection"

if [ "$CLUBDUE_ENV" = "staging" ]; then
  AGREEMENTS_HTML="$(curl -fsS -b "$DEMO_COOKIE_JAR" "${CLUBDUE_URL}/agreements")"
  [[ "$AGREEMENTS_HTML" == *'Maya Thompson'* ]]
  [[ "$AGREEMENTS_HTML" == *'Commercial Grounds Care'* ]]
  [[ "$AGREEMENTS_HTML" == *'Pending renewal'* ]]
  [[ "$AGREEMENTS_HTML" == *'Past due'* ]]
  DEMO_AGREEMENT_COUNT="$(printf '%s' "$AGREEMENTS_HTML" | grep -o 'Open agreement' | wc -l | tr -d ' ')"
  [ "$DEMO_AGREEMENT_COUNT" = "29" ]
  RENEWALS_HTML="$(curl -fsS -b "$DEMO_COOKIE_JAR" "${CLUBDUE_URL}/renewals")"
  [[ "$RENEWALS_HTML" == *'Renewal queue'* ]]
  DEMO_RENEWAL_COUNT="$(printf '%s' "$RENEWALS_HTML" | grep -o 'name="task_id"' | wc -l | tr -d ' ')"
  [ "$DEMO_RENEWAL_COUNT" -le 15 ]
  RENEWAL_TASK_ID="$(printf '%s' "$RENEWALS_HTML" | sed -n 's/.*name="task_id" value="\([^"]*\)".*/\1/p' | head -1)"
  [ -n "$RENEWAL_TASK_ID" ]
  RENEWAL_STATUS="$(curl -sS -b "$DEMO_COOKIE_JAR" -c "$DEMO_COOKIE_JAR" -o /dev/null -w '%{http_code}' -X POST \
    --data-urlencode "csrf_token=$DEMO_CSRF_TOKEN" \
    --data-urlencode "task_id=$RENEWAL_TASK_ID" \
    --data-urlencode 'status=contacted' \
    --data-urlencode 'notes=staging smoke verification' \
    "${CLUBDUE_URL}/renewals")"
  [ "$RENEWAL_STATUS" = "303" ]
  RENEWALS_AFTER_WRITE="$(curl -fsS -b "$DEMO_COOKIE_JAR" "${CLUBDUE_URL}/renewals")"
  [[ "$RENEWALS_AFTER_WRITE" == *'staging smoke verification'* ]]
  VISITS_CSV="$(curl -fsS -b "$DEMO_COOKIE_JAR" "${CLUBDUE_URL}/visits/export.csv")"
  [[ "$VISITS_CSV" == customer_name,* ]]
  echo "  ✓ bounded import, service outcome, renewal note, and CSV export"
else
  OWNER_DEMO_HEADERS="$WORK_DIR/owner-demo.headers"
  OWNER_DEMO_STATUS="$(curl -sS -D "$OWNER_DEMO_HEADERS" -b "$OWNER_COOKIE_JAR" -c "$OWNER_COOKIE_JAR" -o /dev/null -w '%{http_code}' "${CLUBDUE_URL}/demo")"
  [ "$OWNER_DEMO_STATUS" = "303" ]
  grep -Eiq '^Location:[[:space:]]*/dashboard' "$OWNER_DEMO_HEADERS"
  if grep -Eiq '^Set-Cookie:[[:space:]]*clubdue_session=' "$OWNER_DEMO_HEADERS"; then
    echo "authenticated owner demo route replaced the owner session" >&2
    exit 1
  fi
  OWNER_HTML_AFTER="$(curl -fsS -b "$OWNER_COOKIE_JAR" "${CLUBDUE_URL}/dashboard")"
  [[ "$OWNER_HTML_AFTER" == *'Dashboard — ClubDue'* ]]
  [[ "$OWNER_HTML_AFTER" != *'Fictional public demo'* ]]
  OWNER_CUSTOMERS_AFTER="$(curl -fsS -b "$OWNER_COOKIE_JAR" "${CLUBDUE_URL}/customers")"
  [[ "$OWNER_CUSTOMERS_AFTER" != *"$DEMO_SMOKE_NAME"* ]]
  echo "  ✓ production owner tenant unchanged by demo traffic"
fi

echo "All checks passed for ${CLUBDUE_URL}"
