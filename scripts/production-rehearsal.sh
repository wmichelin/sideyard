#!/usr/bin/env bash
# Exercise the production configuration, authenticated dashboard, encrypted
# backup, and restore path locally. It only creates resources in the explicit
# clubdue-production-rehearsal-* namespace and removes them on exit.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NETWORK="clubdue-production-rehearsal-net"
SOURCE_DB="clubdue-production-rehearsal-db"
RESTORE_DB="clubdue-production-rehearsal-restore-db"
APP="clubdue-production-rehearsal-app"
IMAGE="clubdue-production-rehearsal:local"
PORT="18083"
WORK_DIR="$(mktemp -d)"

cleanup() {
  docker rm -f "$APP" "$SOURCE_DB" "$RESTORE_DB" >/dev/null 2>&1 || true
  docker network rm "$NETWORK" >/dev/null 2>&1 || true
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT

# These validation checks run inside a disposable Docker shell. The helper's
# narrow plan seam exercises rollout state selection without a droplet, nginx,
# registry access, or a way to mutate real deployment state.
DEPLOY_TEST_IMAGE="ubuntu:24.04"
run_deploy_helper() {
  docker run --rm --network none \
    -v "$ROOT/scripts/droplet-deploy.sh:/opt/clubdue/droplet-deploy.sh:ro" \
    -e CONTAINER="$1" -e PORT="$2" "${@:3}" \
    "$DEPLOY_TEST_IMAGE" bash /opt/clubdue/droplet-deploy.sh
}

for deployment_target in 'clubdue:8080' 'clubdue:8081' 'clubdue:8082' 'clubdue-staging:8083'; do
  deployment_container="${deployment_target%%:*}"
  deployment_port="${deployment_target##*:}"
  if run_deploy_helper "$deployment_container" "$deployment_port" >/dev/null 2>&1; then
    echo "invalid deployment target ${deployment_target} was accepted" >&2
    exit 1
  fi
done
run_deploy_helper clubdue-staging 8082 -e CLUBDUE_DEPLOY_VALIDATE_ONLY=1 >/dev/null

[ "$(run_deploy_helper clubdue 8083 -e CLUBDUE_DEPLOY_TEST_PLAN=candidate)" = \
  'candidate=18083 active=8083 failure=preserve-active' ]
[ "$(run_deploy_helper clubdue 8083 -e CLUBDUE_DEPLOY_TEST_PLAN=promote)" = \
  'proxy=18083 active=18083 previous=8083' ]
[ "$(run_deploy_helper clubdue 8083 -e CLUBDUE_DEPLOY_TEST_PLAN=next-promote)" = \
  'release-previous=8083 candidate=8083 active=8083 previous=18083' ]
[ "$(run_deploy_helper clubdue 8083 -e CLUBDUE_DEPLOY_TEST_PLAN=rollback)" = \
  'proxy=8083 active=8083 previous=18083' ]

wait_for_postgres() {
  local container="$1"
  for attempt in $(seq 1 30); do
    if docker exec "$container" pg_isready -U clubdue -d clubdue >/dev/null 2>&1; then
      # The official Postgres image briefly accepts connections during its
      # initialization server, then restarts for the final server. Require a
      # second successful probe after that handoff before backup or restore.
      sleep 2
      if docker exec "$container" psql --username=clubdue --dbname=clubdue --command 'SELECT 1' >/dev/null 2>&1; then
        return 0
      fi
    fi
    sleep 1
  done
  echo "Postgres rehearsal container did not become ready" >&2
  return 1
}

wait_for_http() {
  for attempt in $(seq 1 30); do
    if curl -fsS "http://127.0.0.1:${PORT}/readyz" 2>/dev/null | grep -qx ok; then
      return 0
    fi
    sleep 1
  done
  echo "production rehearsal app did not become ready" >&2
  return 1
}

cookie_value() {
  sed -n "s/^Set-Cookie: $1=\([^;]*\).*/\1/pI" "$2" | tail -1
}

docker network create "$NETWORK" >/dev/null
docker run -d --name "$SOURCE_DB" --network "$NETWORK" \
  -e POSTGRES_USER=clubdue -e POSTGRES_PASSWORD=rehearsal-db-password \
  -e POSTGRES_DB=clubdue postgres:16-alpine >/dev/null
wait_for_postgres "$SOURCE_DB"

docker build -t "$IMAGE" "$ROOT" >/dev/null
docker run -d --name "$APP" --network "$NETWORK" -p "127.0.0.1:${PORT}:8080" \
  -e APP_ENV=production \
  -e 'PUBLIC_URL=https://clubdue-production-rehearsal.invalid' \
  -e 'DATABASE_URL=postgres://clubdue:rehearsal-db-password@clubdue-production-rehearsal-db:5432/clubdue?sslmode=disable' \
  -e 'SESSION_SECRET=production-rehearsal-session-secret-0123456789' \
  -e 'BOOTSTRAP_EMAIL=owner@rehearsal.invalid' \
  -e 'BOOTSTRAP_PASSWORD=production-rehearsal-password' \
  -e 'BOOTSTRAP_SHOP_NAME=Production Rehearsal HVAC' \
  "$IMAGE" >/dev/null
wait_for_http

login_html="$WORK_DIR/login.html"
login_headers="$WORK_DIR/login.headers"
login_result_headers="$WORK_DIR/login-result.headers"
curl -fsS -D "$login_headers" -o "$login_html" "http://127.0.0.1:${PORT}/login"
csrf_token="$(sed -n 's/.*name="csrf_token" value="\([^"]*\)".*/\1/p' "$login_html" | head -1)"
csrf_cookie="$(cookie_value clubdue_csrf "$login_headers")"
[ -n "$csrf_token" ] && [ -n "$csrf_cookie" ]

login_status="$(curl -sS -D "$login_result_headers" -o /dev/null -w '%{http_code}' -X POST \
  -H "Cookie: clubdue_csrf=${csrf_cookie}" \
  --data-urlencode "csrf_token=${csrf_token}" \
  --data-urlencode 'email=owner@rehearsal.invalid' \
  --data-urlencode 'password=production-rehearsal-password' \
  "http://127.0.0.1:${PORT}/login")"
[ "$login_status" = "303" ]
session_cookie="$(cookie_value clubdue_session "$login_result_headers")"
[ -n "$session_cookie" ]

dashboard="$(curl -fsS -H "Cookie: clubdue_csrf=${csrf_cookie}; clubdue_session=${session_cookie}" "http://127.0.0.1:${PORT}/dashboard")"
printf '%s' "$dashboard" | grep -q 'Dashboard — ClubDue'
dev_route_status="$(curl -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:${PORT}/dev/seed")"
[ "$dev_route_status" = "404" ]

export CLUBDUE_BACKUP_DB_CONTAINER="$SOURCE_DB"
export CLUBDUE_BACKUP_DIR="$WORK_DIR/backups"
export CLUBDUE_BACKUP_PASSPHRASE="disposable-production-rehearsal-backup-passphrase"
export CLUBDUE_BACKUP_HMAC_KEY="disposable-production-rehearsal-hmac-key"
export CLUBDUE_BACKUP_RETENTION_DAYS=30
mkdir -p "$CLUBDUE_BACKUP_DIR"
expired_backup="$CLUBDUE_BACKUP_DIR/clubdue-postgres-20000101T000000Z.sql.enc"
expired_backup_mac="${expired_backup}.mac"
touch -d '2000-01-01 UTC' "$expired_backup"
touch -d '2000-01-01 UTC' "$expired_backup_mac"
backup_file="$($ROOT/scripts/postgres-backup.sh)"
backup_mac_file="${backup_file}.mac"
[ -s "$backup_file" ]
[ -s "$backup_mac_file" ]
[ ! -e "$expired_backup" ]
[ ! -e "$expired_backup_mac" ]
[ "$(stat -c '%a' "$backup_file")" = "600" ]
[ "$(stat -c '%a' "$backup_mac_file")" = "600" ]
[ "$(stat -c '%s' "$backup_mac_file")" = "32" ]
if openssl enc -d -aes-256-cbc -pbkdf2 -pass env:CLUBDUE_BACKUP_PASSPHRASE -in "$backup_file" | grep -q 'owner@rehearsal.invalid'; then
  :
else
  echo "backup did not contain the rehearsal owner after decryption" >&2
  exit 1
fi
if grep -aq 'owner@rehearsal.invalid' "$backup_file"; then
  echo "backup was not encrypted" >&2
  exit 1
fi

docker run -d --name "$RESTORE_DB" --network "$NETWORK" \
  -e POSTGRES_USER=clubdue -e POSTGRES_PASSWORD=rehearsal-restore-password \
  -e POSTGRES_DB=clubdue postgres:16-alpine >/dev/null
wait_for_postgres "$RESTORE_DB"
export CLUBDUE_RESTORE_DB_CONTAINER="$RESTORE_DB"
"$ROOT/scripts/postgres-restore.sh" "$backup_file"
restored_owner_count="$(docker exec "$RESTORE_DB" psql --username=clubdue --dbname=clubdue --tuples-only --no-align \
  --command "SELECT count(*) FROM users WHERE email = 'owner@rehearsal.invalid'")"
[ "$restored_owner_count" = "1" ]

expect_authenticated_restore_failure() {
  local candidate_backup="$1"
  local failure_output="$WORK_DIR/restore-failure.log"

  if "$ROOT/scripts/postgres-restore.sh" "$candidate_backup" > /dev/null 2>"$failure_output"; then
    echo "tampered backup was restored" >&2
    exit 1
  fi
  grep -qx 'backup authentication failed' "$failure_output"
}

tampered_ciphertext="$WORK_DIR/tampered-ciphertext.sql.enc"
cp "$backup_file" "$tampered_ciphertext"
cp "$backup_mac_file" "${tampered_ciphertext}.mac"
# Appending a byte guarantees a different authenticated ciphertext while
# keeping the original valid MAC sidecar intact.
printf '\000' >> "$tampered_ciphertext"
expect_authenticated_restore_failure "$tampered_ciphertext"

tampered_mac="$WORK_DIR/tampered-mac.sql.enc"
cp "$backup_file" "$tampered_mac"
cp "$backup_mac_file" "${tampered_mac}.mac"
first_mac_byte="$(od -An -tu1 -N1 "${tampered_mac}.mac" | tr -d '[:space:]')"
if [ "$first_mac_byte" = "0" ]; then
  replacement_mac_byte=1
else
  replacement_mac_byte=0
fi
printf "\\$(printf '%03o' "$replacement_mac_byte")" \
  | dd of="${tampered_mac}.mac" bs=1 count=1 conv=notrunc status=none
expect_authenticated_restore_failure "$tampered_mac"

restored_owner_count_after_tamper_tests="$(docker exec "$RESTORE_DB" psql --username=clubdue --dbname=clubdue --tuples-only --no-align \
  --command "SELECT count(*) FROM users WHERE email = 'owner@rehearsal.invalid'")"
[ "$restored_owner_count_after_tamper_tests" = "1" ]

echo "production rehearsal passed: readiness, authenticated encrypted backup, restore, and tamper rejection"
