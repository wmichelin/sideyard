#!/usr/bin/env bash
# Exercise the logical-export role against a disposable PostgreSQL cluster.
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
container="sideyard-postgres-exporter-rehearsal-$$"
network="${container}-net"
export_volume="${container}-export"
work="$(mktemp -d)"
admin_password="$(printf 'e%.0s' {1..48})"
cleanup() {
  [ -z "${kill_container:-}" ] || docker rm -f "$kill_container" >/dev/null 2>&1 || true
  docker rm -f "$container" >/dev/null 2>&1 || true
  docker network rm "$network" >/dev/null 2>&1 || true
  docker volume rm "$export_volume" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

docker run --rm -e GOTOOLCHAIN=local -e CGO_ENABLED=0 \
  -v sideyard-go-modules:/go/pkg/mod \
  -v sideyard-go-build:/root/.cache/go-build \
  -v "$repo_root:/src:ro" -v "$work:/output" -w /src \
  golang:1.23-alpine sh -c 'go test -c -o /output/postgres.test ./internal/postgres && go build -o /output/clubdue ./cmd/clubdue'
docker network create --internal "$network" >/dev/null
docker volume create "$export_volume" >/dev/null
docker run --rm -v "$export_volume:/var/lib/sideyard-logical-export" alpine:3.21 \
  chmod 700 /var/lib/sideyard-logical-export
docker run -d --name "$container" --network "$network" --network-alias clubdue-staging-db \
  -e POSTGRES_DB=clubdue -e POSTGRES_USER=clubdue -e "POSTGRES_PASSWORD=${admin_password}" \
  postgres:16-alpine >/dev/null
ready=false
for _ in $(seq 1 30); do
  if docker run --rm --network "$network" postgres:16-alpine \
      pg_isready -h clubdue-staging-db -U clubdue -d clubdue >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 1
done
[ "$ready" = true ] || {
  echo 'Disposable PostgreSQL did not become reachable from its isolated client network' >&2
  docker logs "$container" >&2
  exit 1
}
docker run --rm --network "$network" \
  -e "DATABASE_URL=postgres://clubdue:${admin_password}@clubdue-staging-db:5432/clubdue?sslmode=disable" \
  -e CLUBDUE_REQUIRE_POSTGRES_INTEGRATION=1 \
  -e CLUBDUE_REQUIRE_POSTGRES_EXPORTER_TEST=1 \
  -v "$work/postgres.test:/postgres.test:ro" \
  golang:1.23-alpine /postgres.test -test.count=1 -test.v -test.run '^TestLogicalExporterPermissions$'

operator_env=(
  -e APP_ENV=staging -e DATABASE_BACKEND=postgres
  -e DATA_GENERATION=123e4567-e89b-42d3-a456-426614174000
  -e "DATABASE_URL=postgres://clubdue:${admin_password}@clubdue-staging-db:5432/clubdue?sslmode=disable"
)
for command in bootstrap-postgres-exporter check-postgres-exporter; do
  docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges \
    --network "$network" "${operator_env[@]}" \
    -v "$work/clubdue:/clubdue:ro" \
    alpine:3.21 /clubdue "$command"
done
mkdir -m 700 "$work/host-bin" "$work/host-lock" "$work/host-spool" "$work/host-state"
cat > "$work/host-state/staging-backend.env" <<EOF
DATABASE_BACKEND=postgres
DATA_GENERATION=123e4567-e89b-42d3-a456-426614174000
EOF
chmod 600 "$work/host-state/staging-backend.env"
cat > "$work/staging-db.env" <<EOF
POSTGRES_PASSWORD=${admin_password}
SESSION_SECRET=$(printf 'f%.0s' {1..64})
EOF
chmod 600 "$work/staging-db.env"
real_docker="$(command -v docker)"
cat > "$work/host-bin/docker" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
if [ "$1" = inspect ]; then
  case "$3" in
    *State.Running*) echo true ;;
    *sideyard.backend*) echo postgres ;;
    *sideyard.generation*) echo 123e4567-e89b-42d3-a456-426614174000 ;;
    *org.opencontainers.image.revision*) echo bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb ;;
    *Config.Image*) echo ghcr.io/wmichelin/clubdue@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa ;;
    *) exec "$SIDEYARD_REAL_DOCKER" "$@" ;;
  esac
  exit 0
fi
arguments=()
for argument in "$@"; do
  if [ "$argument" = ghcr.io/wmichelin/clubdue@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa ]; then
    arguments+=(-v "$SIDEYARD_TEST_BINARY:/clubdue:ro" alpine:3.21 /clubdue)
  else
    arguments+=("$argument")
  fi
done
exec "$SIDEYARD_REAL_DOCKER" "${arguments[@]}"
SH
chmod 700 "$work/host-bin/docker"
run_supervisor() {
  exec env PATH="$work/host-bin:$PATH" SIDEYARD_REAL_DOCKER="$real_docker" SIDEYARD_TEST_BINARY="$work/clubdue" \
  SIDEYARD_EXPORTER_TEST_MODE=1 SIDEYARD_EXPORTER_APP_CONTAINER=test-app SIDEYARD_EXPORTER_DB_CONTAINER="$container" \
  SIDEYARD_EXPORTER_NETWORK="$network" SIDEYARD_EXPORTER_SPOOL="$work/host-spool" \
  SIDEYARD_EXPORTER_DATABASE_ENV="$work/staging-db.env" SIDEYARD_RUNTIME_STATE_SCRIPT="$repo_root/scripts/azure/runtime-state.sh" \
  SIDEYARD_STATE_ROOT="$work/host-state" SIDEYARD_STATE_TEST_OWNER=1 SIDEYARD_LOCK_ROOT="$work/host-lock" \
  SIDEYARD_EXPECTED_REVISION=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb \
  SIDEYARD_AZURE_SUBSCRIPTION_ID=11111111-1111-1111-1111-111111111111 SIDEYARD_PHOTO_ACCOUNT=sideyardstg1234 \
  SIDEYARD_EXPORTER_FAILPOINT="${SIDEYARD_EXPORTER_FAILPOINT:-}" \
  bash "$repo_root/scripts/azure/run-logical-postgres-export.sh" staging export
}
supervisor_log="$work/supervisor.log"
SIDEYARD_EXPORTER_FAILPOINT=after-activation-before-journal run_supervisor >"$supervisor_log" 2>&1 &
supervisor_pid=$!
activated=false
for _ in $(seq 1 30); do
  if grep -q '"phase":"activating"' "$work/host-spool/operator-journal.json" 2>/dev/null; then activated=true; break; fi
  sleep 1
done
[ "$activated" = true ] || {
  echo 'Supervisor did not reach the activating crash checkpoint' >&2
  cat "$supervisor_log" >&2
  exit 1
}
kill -KILL "$supervisor_pid"
wait "$supervisor_pid" 2>/dev/null || true
unset SIDEYARD_EXPORTER_FAILPOINT
logical_result="$(run_supervisor)"
printf '%s' "$logical_result" | python3 -c 'import json,sys; value=json.load(sys.stdin); assert value["package_bytes"]>0; assert len(value["package_sha256"])==64'
python3 - "$work/host-spool/operator-journal.json" <<'PY'
import json,sys
with open(sys.argv[1]) as source: value=json.load(source)
assert value["phase"]=="package_ready"
PY
docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges \
  --network "$network" "${operator_env[@]}" -v "$work/clubdue:/clubdue:ro" alpine:3.21 /clubdue check-postgres-exporter
bash "$repo_root/scripts/azure/test-postgres-exporter-access.sh"
bash "$repo_root/scripts/azure/test-logical-postgres-export.sh"
echo 'PostgreSQL logical exporter rehearsal passed'
