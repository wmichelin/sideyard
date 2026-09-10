#!/usr/bin/env bash
# Exercise the logical-export role against a disposable PostgreSQL cluster.
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
container="sideyard-postgres-exporter-rehearsal-$$"
network="${container}-net"
work="$(mktemp -d)"
cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
  docker network rm "$network" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

docker run --rm -e GOTOOLCHAIN=local -e CGO_ENABLED=0 \
  -v sideyard-go-modules:/go/pkg/mod \
  -v sideyard-go-build:/root/.cache/go-build \
  -v "$repo_root:/src:ro" -v "$work:/output" -w /src \
  golang:1.23-alpine sh -c 'go test -c -o /output/postgres.test ./internal/postgres && go build -o /output/clubdue ./cmd/clubdue'
docker network create --internal "$network" >/dev/null
docker run -d --name "$container" --network "$network" --network-alias clubdue-staging-db \
  -e POSTGRES_DB=clubdue -e POSTGRES_USER=clubdue -e POSTGRES_PASSWORD=exporter-rehearsal-password \
  postgres:16-alpine >/dev/null
ready=false
for _ in $(seq 1 30); do
  if docker exec "$container" pg_isready -U clubdue -d clubdue >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 1
done
[ "$ready" = true ] || { echo 'Disposable PostgreSQL did not become ready' >&2; exit 1; }
docker run --rm --network "$network" \
  -e DATABASE_URL='postgres://clubdue:exporter-rehearsal-password@clubdue-staging-db:5432/clubdue?sslmode=disable' \
  -e CLUBDUE_REQUIRE_POSTGRES_INTEGRATION=1 \
  -e CLUBDUE_REQUIRE_POSTGRES_EXPORTER_TEST=1 \
  -v "$work/postgres.test:/postgres.test:ro" \
  golang:1.23-alpine /postgres.test -test.count=1 -test.v -test.run '^TestLogicalExporterPermissions$'

operator_env=(
  -e APP_ENV=staging -e DATABASE_BACKEND=postgres
  -e DATA_GENERATION=123e4567-e89b-42d3-a456-426614174000
  -e 'DATABASE_URL=postgres://clubdue:exporter-rehearsal-password@clubdue-staging-db:5432/clubdue?sslmode=disable'
)
for command in bootstrap-postgres-exporter check-postgres-exporter; do
  docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges \
    --network "$network" "${operator_env[@]}" \
    -v "$work/clubdue:/clubdue:ro" \
    alpine:3.21 /clubdue "$command"
done
bash "$repo_root/scripts/azure/test-postgres-exporter-access.sh"
echo 'PostgreSQL logical exporter rehearsal passed'
