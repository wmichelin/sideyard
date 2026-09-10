#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
cleanup() { rm -rf "$WORK"; }
trap cleanup EXIT
mkdir -m 700 "$WORK/bin" "$WORK/lock" "$WORK/state"
generation=123e4567-e89b-42d3-a456-426614174000
revision=0123456789abcdef0123456789abcdef01234567
digest="ghcr.io/wmichelin/clubdue@sha256:$(printf 'a%.0s' {1..64})"
cat > "$WORK/bin/docker" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
if [ "$1" = inspect ]; then
  template="$3"
  case "$template" in
    *State.Running*) echo true ;;
    *sideyard.backend*) echo postgres ;;
    *sideyard.generation*) echo 123e4567-e89b-42d3-a456-426614174000 ;;
    *org.opencontainers.image.revision*) echo "${SIDEYARD_TEST_REVISION}" ;;
    *Config.Image*) echo "${SIDEYARD_TEST_IMAGE}" ;;
    *) exit 1 ;;
  esac
  exit 0
fi
if [ "$1" = run ]; then
  printf '%s\n' "$*" >> "$SIDEYARD_TEST_DOCKER_LOG"
  echo 'Dormant PostgreSQL staging logical exporter verified'
  exit 0
fi
exit 1
SH
chmod 700 "$WORK/bin/docker"
cat > "$WORK/state/staging-backend.env" <<EOF
DATABASE_BACKEND=postgres
DATA_GENERATION=${generation}
EOF
chmod 600 "$WORK/state/staging-backend.env"
cat > "$WORK/staging-db.env" <<EOF
POSTGRES_PASSWORD=$(printf 'b%.0s' {1..48})
EOF
chmod 600 "$WORK/staging-db.env"

run_wrapper() {
  PATH="$WORK/bin:$PATH" \
  SIDEYARD_EXPORTER_TEST_MODE=1 \
  SIDEYARD_EXPORTER_APP_CONTAINER=test-app \
  SIDEYARD_EXPORTER_DB_CONTAINER=test-db \
  SIDEYARD_EXPORTER_DATABASE_ENV="$WORK/staging-db.env" \
  SIDEYARD_RUNTIME_STATE_SCRIPT="$ROOT/scripts/azure/runtime-state.sh" \
  SIDEYARD_STATE_ROOT="$WORK/state" SIDEYARD_STATE_TEST_OWNER=1 \
  SIDEYARD_LOCK_ROOT="$WORK/lock" SIDEYARD_EXPECTED_REVISION="${SIDEYARD_EXPECTED_REVISION:-$revision}" \
  SIDEYARD_TEST_REVISION="${SIDEYARD_TEST_REVISION:-$revision}" SIDEYARD_TEST_IMAGE="${SIDEYARD_TEST_IMAGE:-$digest}" \
  SIDEYARD_TEST_DOCKER_LOG="$WORK/docker.log" \
  bash "$ROOT/scripts/azure/run-postgres-exporter-access.sh" staging "$1"
}

output="$(run_wrapper bootstrap)"
grep -q 'bootstrap-postgres-exporter' "$WORK/docker.log"
case "$output" in *bbbbbbbb*) echo 'database secret leaked to output' >&2; exit 1 ;; esac
: > "$WORK/docker.log"
run_wrapper check >/dev/null
grep -q 'check-postgres-exporter' "$WORK/docker.log"

: > "$WORK/docker.log"
if SIDEYARD_EXPECTED_REVISION=ffffffffffffffffffffffffffffffffffffffff run_wrapper check >/dev/null 2>&1; then
  echo 'mismatched staging revision was accepted' >&2
  exit 1
fi
[ ! -s "$WORK/docker.log" ] || { echo 'operator ran after revision rejection' >&2; exit 1; }

: > "$WORK/docker.log"
if SIDEYARD_TEST_IMAGE=clubdue:mutable run_wrapper check >/dev/null 2>&1; then
  echo 'mutable staging image was accepted' >&2
  exit 1
fi
[ ! -s "$WORK/docker.log" ] || { echo 'operator ran after image rejection' >&2; exit 1; }

sed -i 's/DATABASE_BACKEND=postgres/DATABASE_BACKEND=azure_sql/' "$WORK/state/staging-backend.env"
: > "$WORK/docker.log"
if run_wrapper check >/dev/null 2>&1; then
  echo 'non-PostgreSQL authority was accepted' >&2
  exit 1
fi
[ ! -s "$WORK/docker.log" ] || { echo 'operator ran after backend rejection' >&2; exit 1; }
sed -i 's/DATABASE_BACKEND=azure_sql/DATABASE_BACKEND=postgres/' "$WORK/state/staging-backend.env"

chmod 644 "$WORK/staging-db.env"
: > "$WORK/docker.log"
if run_wrapper check >/dev/null 2>&1; then
  echo 'world-readable database environment was accepted' >&2
  exit 1
fi
[ ! -s "$WORK/docker.log" ] || { echo 'operator ran after file-mode rejection' >&2; exit 1; }

echo 'PostgreSQL exporter host boundary checks passed'
