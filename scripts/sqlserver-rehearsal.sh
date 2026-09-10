#!/usr/bin/env bash
# Only disposable, isolated local SQL Server resources are created. No Azure
# credentials, published ports or production connection strings are used.
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_name="sideyard-sqlserver-rehearsal-$$"
test_work="$(mktemp -d)"
cleanup() {
  docker rm -f "$test_name" >/dev/null 2>&1 || true
  docker network rm "$test_name" >/dev/null 2>&1 || true
  rm -rf "$test_work"
}
trap cleanup EXIT
umask 077
python3 - "$test_work" "$test_name" <<'PY'
import pathlib, secrets, sys
path = pathlib.Path(sys.argv[1])
password = secrets.token_urlsafe(30) + "!Aa1"
(path / "server.env").write_text("ACCEPT_EULA=Y\nMSSQL_PID=Developer\nMSSQL_MEMORY_LIMIT_MB=2048\nMSSQL_SA_PASSWORD=" + password + "\n")
(path / "test.env").write_text("CLUBDUE_REQUIRE_SQLSERVER_INTEGRATION=1\nSQLSERVER_TEST_DSN=server=" + sys.argv[2] + ";database=sideyard_test;user id=sa;password=" + password + ";encrypt=true;TrustServerCertificate=true;disableretry=true\n")
PY
# Compile while public module downloads are available; engine tests then need
# only the isolated database network, including on hosts that restrict bridge
# egress. The test binary and synthetic credential files are removed on exit.
docker run --rm -e GOTOOLCHAIN=local -e CGO_ENABLED=0 \
  -v sideyard-go-modules:/go/pkg/mod \
  -v sideyard-go-build:/root/.cache/go-build \
  -v "$repo_root:/src:ro" -v "$test_work:/output" -w /src \
  golang:1.23-alpine sh -c 'go test -c -o /output/sqlserver.test ./internal/sqlserver && go test -c -o /output/app.test ./internal/app'
docker network create --internal "$test_name" >/dev/null
docker run -d --name "$test_name" --network "$test_name" --memory 4g --cpus 2 \
  --env-file "$test_work/server.env" \
  mcr.microsoft.com/mssql/server@sha256:97b448857967be55e005424a660056fe6d51814435804dc07e8f79f028bab5fb >/dev/null
ready=false
for attempt in $(seq 1 60); do
  if docker exec "$test_name" sh -c \
    'SQLCMDPASSWORD="$MSSQL_SA_PASSWORD" /opt/mssql-tools18/bin/sqlcmd -S localhost -U sa -C -b -Q "SELECT 1"' >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 1
done
if [[ "$ready" != true ]]; then
  echo "Disposable SQL Server did not become ready" >&2
  exit 1
fi
docker exec "$test_name" sh -c \
  'SQLCMDPASSWORD="$MSSQL_SA_PASSWORD" /opt/mssql-tools18/bin/sqlcmd -S localhost -U sa -C -b -Q "CREATE DATABASE sideyard_test"' >/dev/null
docker run --rm --network "$test_name" --env-file "$test_work/test.env" \
  -v "$test_work/sqlserver.test:/sqlserver.test:ro" \
  golang:1.23-alpine /sqlserver.test -test.count=1 -test.v
docker run --rm --network "$test_name" --env-file "$test_work/test.env" \
  -v "$test_work/app.test:/app.test:ro" \
  -v "$repo_root/internal/app/testdata:/work/testdata:ro" -w /work \
  golang:1.23-alpine /app.test -test.count=1 -test.v -test.run '^TestSQLServer'
echo "SQL Server connection and migration rehearsal passed"
