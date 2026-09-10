#!/usr/bin/env bash
# Bootstrap or audit the dormant staging PostgreSQL logical-export roles from
# the exact running application artifact. This command creates no credential.
set -euo pipefail

environment="${1:-}"
action="${2:-check}"
[ "$environment" = staging ] || { echo 'PostgreSQL logical exporter is staging-only' >&2; exit 64; }
case "$action" in bootstrap|check) ;; *) echo 'usage: run-postgres-exporter-access.sh staging bootstrap|check' >&2; exit 64 ;; esac
if [ -z "${SIDEYARD_EXPORTER_TEST_MODE:-}" ]; then
  [ "$(id -u)" = 0 ] || { echo 'PostgreSQL logical exporter requires root' >&2; exit 77; }
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Installed operations use this script beside the shared state helper. Tests may
# point at an isolated copy through SIDEYARD_RUNTIME_STATE_SCRIPT.
runtime_state="${SIDEYARD_RUNTIME_STATE_SCRIPT:-${SCRIPT_DIR}/runtime-state.sh}"
[ -f "$runtime_state" ] && [ ! -L "$runtime_state" ] || { echo 'Sideyard runtime-state helper is missing or unsafe' >&2; exit 66; }
# shellcheck source=runtime-state.sh
source "$runtime_state"
if [ "${SIDEYARD_DATABASE_LOCK_HELD:-}" = 1 ]; then
  flock -n 9 || { echo 'inherited staging database lock is invalid' >&2; exit 75; }
else
  prepare_sideyard_lock_root
  exec 9>"${SIDEYARD_LOCK_ROOT}/sideyard-staging-database-window.lock"
  flock -w 120 9 || { echo 'another staging database or deployment operation is active' >&2; exit 75; }
fi
load_sideyard_runtime_state staging
[ "$SIDEYARD_BACKEND" = postgres ] || { echo 'PostgreSQL is not the authoritative staging source' >&2; exit 65; }

app_container="${SIDEYARD_EXPORTER_APP_CONTAINER:-clubdue-staging-current}"
db_container="${SIDEYARD_EXPORTER_DB_CONTAINER:-clubdue-staging-db}"
if [ -z "${SIDEYARD_EXPORTER_TEST_MODE:-}" ]; then
  [ "$app_container" = clubdue-staging-current ] && [ "$db_container" = clubdue-staging-db ] || {
    echo 'PostgreSQL exporter container target is invalid' >&2; exit 64;
  }
fi
verify_sideyard_current_container staging "$app_container"
[ "$(docker inspect -f '{{.State.Running}}' "$db_container" 2>/dev/null || true)" = true ] || {
  echo 'staging PostgreSQL container is not running' >&2; exit 69;
}

revision="$(docker inspect -f '{{ index .Config.Labels "org.opencontainers.image.revision" }}' "$app_container")"
[[ "$revision" =~ ^[0-9a-f]{40}$ ]] || { echo 'running staging revision is invalid' >&2; exit 65; }
if [ -n "${SIDEYARD_EXPECTED_REVISION:-}" ] && [ "$revision" != "$SIDEYARD_EXPECTED_REVISION" ]; then
  echo 'running staging revision does not match the reviewed workflow revision' >&2
  exit 65
fi
image="$(docker inspect -f '{{.Config.Image}}' "$app_container")"
[[ "$image" =~ ^ghcr.io/wmichelin/clubdue@sha256:[0-9a-f]{64}$ ]] || {
  echo 'running staging image is not immutable' >&2; exit 65;
}

database_env="${SIDEYARD_EXPORTER_DATABASE_ENV:-/opt/clubdue/staging-db.env}"
[ -f "$database_env" ] && [ ! -L "$database_env" ] || { echo 'staging database environment file is missing or unsafe' >&2; exit 66; }
env_mode="$(stat -c '%a' "$database_env")"
env_owner="$(stat -c '%u:%g' "$database_env")"
[ $((8#$env_mode & 077)) -eq 0 ] || { echo 'staging database environment file must be owner-only' >&2; exit 77; }
if [ -z "${SIDEYARD_EXPORTER_TEST_MODE:-}" ]; then
  [ "$env_owner" = 0:0 ] || { echo 'staging database environment file must be root-owned' >&2; exit 77; }
fi
set -a
# shellcheck disable=SC1090
source "$database_env"
set +a
: "${POSTGRES_PASSWORD:?POSTGRES_PASSWORD required}"
[[ "$POSTGRES_PASSWORD" =~ ^[0-9a-f]{48}$ ]] || { echo 'staging PostgreSQL password format is invalid' >&2; exit 65; }

command="check-postgres-exporter"
[ "$action" = bootstrap ] && command="bootstrap-postgres-exporter"
docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges \
  --network clubdue-staging-net \
  -e APP_ENV=staging -e DATABASE_BACKEND=postgres -e "DATA_GENERATION=${SIDEYARD_GENERATION}" \
  -e "DATABASE_URL=postgres://clubdue:${POSTGRES_PASSWORD}@clubdue-staging-db:5432/clubdue?sslmode=disable" \
  "$image" "$command"

echo "PostgreSQL logical exporter ${action} passed for revision ${revision}"
