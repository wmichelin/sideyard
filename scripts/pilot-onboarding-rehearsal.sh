#!/usr/bin/env bash
# Rehearse a fictional, fresh-shop onboarding flow. Only the explicit
# clubdue-pilot-rehearsal-* Docker namespace is created and it is removed on exit.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NETWORK="clubdue-pilot-rehearsal-net"
DB="clubdue-pilot-rehearsal-db"
APP="clubdue-pilot-rehearsal-app"
IMAGE="clubdue-pilot-rehearsal:local"
PORT=18084
WORK_DIR="$(mktemp -d)"
FIXTURE="$ROOT/internal/app/testdata/pilot-concierge-import.csv"

cleanup() {
  docker rm -f "$APP" "$DB" >/dev/null 2>&1 || true
  docker network rm "$NETWORK" >/dev/null 2>&1 || true
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT

wait_for() {
  for attempt in $(seq 1 30); do
    if curl -fsS "http://127.0.0.1:${PORT}/readyz" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  echo "pilot onboarding rehearsal app did not become ready" >&2
  return 1
}

wait_for_postgres() {
  for attempt in $(seq 1 30); do
    if docker exec "$DB" pg_isready -U clubdue -d clubdue >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  echo "pilot onboarding rehearsal database did not become ready" >&2
  return 1
}

docker network create "$NETWORK" >/dev/null
docker run -d --name "$DB" --network "$NETWORK" \
  -e POSTGRES_USER=clubdue -e POSTGRES_PASSWORD=pilot-rehearsal-password -e POSTGRES_DB=clubdue \
  postgres:16-alpine >/dev/null
wait_for_postgres
docker build -t "$IMAGE" "$ROOT" >/dev/null
docker run -d --name "$APP" --network "$NETWORK" -p "127.0.0.1:${PORT}:8080" \
  -e APP_ENV=development \
  -e DATABASE_URL="postgres://clubdue:pilot-rehearsal-password@${DB}:5432/clubdue?sslmode=disable" \
  -e SESSION_SECRET=pilot-rehearsal-session-secret-0123456789 \
  -e BOOTSTRAP_EMAIL=owner@pilot-rehearsal.invalid \
  -e BOOTSTRAP_PASSWORD=pilot-rehearsal-password \
  -e BOOTSTRAP_SHOP_NAME='Pilot Rehearsal HVAC' "$IMAGE" >/dev/null
wait_for
started_at=$(date +%s)

cookie="$WORK_DIR/cookies"
login_html="$WORK_DIR/login.html"
curl -fsS -c "$cookie" -o "$login_html" "http://127.0.0.1:${PORT}/login"
csrf="$(sed -n 's/.*name="csrf_token" value="\([^"]*\)".*/\1/p' "$login_html" | head -1)"
[ -n "$csrf" ]
login_status="$(curl -sS -b "$cookie" -c "$cookie" -o /dev/null -w '%{http_code}' -X POST \
  --data-urlencode "csrf_token=$csrf" --data-urlencode 'email=owner@pilot-rehearsal.invalid' \
  --data-urlencode 'password=pilot-rehearsal-password' "http://127.0.0.1:${PORT}/login")"
[ "$login_status" = 303 ]

plan_status="$(curl -sS -b "$cookie" -c "$cookie" -o /dev/null -w '%{http_code}' -X POST \
  --data-urlencode "csrf_token=$csrf" --data-urlencode 'name=Essential HVAC Care' \
  --data-urlencode 'price_cents=19900' --data-urlencode 'term_months=12' \
  --data-urlencode 'visits_per_year=2' --data-urlencode 'description=Fictional pilot plan' \
  "http://127.0.0.1:${PORT}/plan-templates")"
[ "$plan_status" = 303 ]

preview="$WORK_DIR/preview.html"
curl -fsS -b "$cookie" -c "$cookie" -o "$preview" -X POST \
  -F "csrf_token=$csrf" -F step=dry_run -F "csv=@${FIXTURE};type=text/csv" "http://127.0.0.1:${PORT}/import"
grep -q 'Import preview' "$preview"
csv_data="$(sed -n 's/.*name="csv_data" value="\([^"]*\)".*/\1/p' "$preview" | head -1)"
[ -n "$csv_data" ]
commit="$WORK_DIR/commit.html"
curl -fsS -b "$cookie" -c "$cookie" -o "$commit" -X POST \
  --data-urlencode "csrf_token=$csrf" --data-urlencode step=commit --data-urlencode "csv_data=$csv_data" "http://127.0.0.1:${PORT}/import"
grep -q 'Import complete' "$commit"

revised="$WORK_DIR/revised.csv"
sed 's/,19900$/,20900/' "$FIXTURE" > "$revised"
curl -fsS -b "$cookie" -c "$cookie" -o "$preview" -X POST \
  -F "csrf_token=$csrf" -F step=dry_run -F "csv=@${revised};type=text/csv" "http://127.0.0.1:${PORT}/import"
csv_data="$(sed -n 's/.*name="csv_data" value="\([^"]*\)".*/\1/p' "$preview" | head -1)"
curl -fsS -b "$cookie" -c "$cookie" -o "$commit" -X POST \
  --data-urlencode "csrf_token=$csrf" --data-urlencode step=commit --data-urlencode "csv_data=$csv_data" "http://127.0.0.1:${PORT}/import"
grep -q '3 updated agreements' "$commit"
agreements="$(curl -fsS -b "$cookie" "http://127.0.0.1:${PORT}/agreements")"
[[ "$agreements" == *'Avery Morgan'* ]]
export_csv="$(curl -fsS -b "$cookie" "http://127.0.0.1:${PORT}/visits/export.csv")"
[[ "$export_csv" == customer_name,* ]]

elapsed=$(( $(date +%s) - started_at ))
[ "$elapsed" -lt 900 ]
printf 'pilot onboarding rehearsal passed: import reconciliation, correction, export, first value in %ss\n' "$elapsed"
