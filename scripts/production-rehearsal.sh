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

# These validation checks execute before registry, package, Docker, or droplet
# work in the shared deployment helper.
for deployment_target in 'clubdue:8080' 'clubdue:8081' 'clubdue:8082' 'clubdue-staging:8083'; do
  deployment_container="${deployment_target%%:*}"
  deployment_port="${deployment_target##*:}"
  if env CONTAINER="$deployment_container" PORT="$deployment_port" bash -c "$(<"$ROOT/scripts/droplet-deploy.sh")" >/dev/null 2>&1; then
    echo "invalid deployment target ${deployment_target} was accepted" >&2
    exit 1
  fi
done

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
export CLUBDUE_BACKUP_RETENTION_DAYS=30
mkdir -p "$CLUBDUE_BACKUP_DIR"
expired_backup="$CLUBDUE_BACKUP_DIR/clubdue-postgres-20000101T000000Z.sql.enc"
touch -d '2000-01-01 UTC' "$expired_backup"
backup_file="$($ROOT/scripts/postgres-backup.sh)"
[ -s "$backup_file" ]
[ ! -e "$expired_backup" ]
[ "$(stat -c '%a' "$backup_file")" = "600" ]
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

echo "production rehearsal passed: readiness, authenticated dashboard, encrypted backup, and restore"
