#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
cleanup() { rm -rf "$WORK"; }
trap cleanup EXIT
mkdir -m 700 "$WORK/bin" "$WORK/lock" "$WORK/state" "$WORK/spool"
generation=123e4567-e89b-42d3-a456-426614174000
revision=0123456789abcdef0123456789abcdef01234567
digest="ghcr.io/wmichelin/clubdue@sha256:$(printf 'a%.0s' {1..64})"
cat > "$WORK/bin/docker" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
if [ "$1" = inspect ]; then
  case "$3" in
    *State.Running*) echo true ;;
    *sideyard.backend*) echo postgres ;;
    *sideyard.generation*) echo 123e4567-e89b-42d3-a456-426614174000 ;;
    *org.opencontainers.image.revision*) echo 0123456789abcdef0123456789abcdef01234567 ;;
    *Config.Image*) echo "ghcr.io/wmichelin/clubdue@sha256:$(printf 'a%.0s' {1..64})" ;;
    *) exit 1 ;;
  esac
  exit 0
fi
printf '%s\n' "$*" >> "$SIDEYARD_TEST_DOCKER_LOG"
if [[ "$*" == *export-postgres-logical* ]]; then
  python3 - "$SIDEYARD_TEST_SPOOL/operator-journal.json" <<'PY'
import json,os,sys
path=sys.argv[1]
with open(path) as source: value=json.load(source)
value["phase"]="activated"; value["server_deadline"]="2026-09-11T03:00:00Z"
with open(path,"w") as output: json.dump(value,output,separators=(",",":")); output.write("\n")
os.chmod(path,0o600)
PY
  if [ "${SIDEYARD_TEST_EXPORT_FAIL:-}" = 1 ]; then exit 42; fi
  export_id=aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee
  target="$SIDEYARD_TEST_SPOOL/recovery-${export_id}.tar"
  printf 'verified synthetic logical package\n' > "$target"
  chmod 600 "$target"
  sha="$(sha256sum "$target" | cut -d' ' -f1)"
  size="$(stat -c %s "$target")"
  printf '{"path":"/var/lib/sideyard-logical-export/recovery-%s.tar","export_id":"%s","package_sha256":"%s","package_bytes":%s,"root_sha256":"%s"}\n' \
    "$export_id" "$export_id" "$sha" "$size" "$(printf 'b%.0s' {1..64})"
else
  echo 'PostgreSQL staging logical exporter restricted'
fi
SH
chmod 700 "$WORK/bin/docker"
cat > "$WORK/state/staging-backend.env" <<EOF
DATABASE_BACKEND=postgres
DATA_GENERATION=${generation}
EOF
chmod 600 "$WORK/state/staging-backend.env"
cat > "$WORK/staging-db.env" <<EOF
POSTGRES_PASSWORD=$(printf 'c%.0s' {1..48})
SESSION_SECRET=$(printf 'd%.0s' {1..64})
EOF
chmod 600 "$WORK/staging-db.env"

run_export() {
  PATH="$WORK/bin:$PATH" SIDEYARD_EXPORTER_TEST_MODE=1 \
  SIDEYARD_EXPORTER_APP_CONTAINER=test-app SIDEYARD_EXPORTER_DB_CONTAINER=test-db \
  SIDEYARD_EXPORTER_NETWORK=test-net SIDEYARD_EXPORTER_SPOOL="$WORK/spool" \
  SIDEYARD_EXPORTER_DATABASE_ENV="$WORK/staging-db.env" \
  SIDEYARD_RUNTIME_STATE_SCRIPT="$ROOT/scripts/azure/runtime-state.sh" \
  SIDEYARD_STATE_ROOT="$WORK/state" SIDEYARD_STATE_TEST_OWNER=1 SIDEYARD_LOCK_ROOT="$WORK/lock" \
  SIDEYARD_EXPECTED_REVISION="$revision" SIDEYARD_AZURE_SUBSCRIPTION_ID=11111111-1111-1111-1111-111111111111 \
  SIDEYARD_PHOTO_ACCOUNT=sideyardstg1234 SIDEYARD_TEST_DOCKER_LOG="$WORK/docker.log" \
  SIDEYARD_TEST_SPOOL="$WORK/spool" bash "$ROOT/scripts/azure/run-logical-postgres-export.sh" staging "${1:-export}"
}

# A crash before the initial journal rename leaves only the temporary file. The
# next invocation must restrict the database role before beginning a new export.
printf '{"interrupted":"transition"}\n' > "$WORK/spool/operator-journal.json.new"
chmod 600 "$WORK/spool/operator-journal.json.new"
result="$(run_export export)"
printf '%s' "$result" | python3 -c 'import json,sys; assert json.load(sys.stdin)["export_id"]=="aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"'
python3 - "$WORK/spool/operator-journal.json" <<'PY'
import json,sys
with open(sys.argv[1]) as source: value=json.load(source)
assert value["phase"] == "package_ready"
PY
grep -q 'export-postgres-logical' "$WORK/docker.log"
grep -q 'reconcile-postgres-exporter' "$WORK/docker.log"
[ ! -e "$WORK/spool/operator-journal.json.new" ]
if grep -q "$(printf 'c%.0s' {1..48})" "$WORK/docker.log"; then echo 'database secret leaked into Docker arguments' >&2; exit 1; fi
[ ! -e "$WORK/spool/.database-secret.env" ]
if run_export export >/dev/null 2>&1; then echo 'uncollected package was overwritten' >&2; exit 1; fi
[ -f "$WORK/spool/recovery-aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee.tar" ]

python3 - "$WORK/spool" <<'PY'
from pathlib import Path
import json,os,sys
root=Path(sys.argv[1]); (root/'recovery-aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee.tar').unlink()
value=json.loads((root/'operator-journal.json').read_text()); value={k:value[k] for k in ('format','environment','generation','revision','image','attempt')}; value['phase']='pre_activation'
(root/'operator-journal.json').write_text(json.dumps(value,separators=(',',':'))+'\n'); os.chmod(root/'operator-journal.json',0o600)
(root/'operator-journal.json.new').write_text('{"interrupted":"transition"}\n'); os.chmod(root/'operator-journal.json.new',0o600)
(root/'.database-partial').write_text('partial'); os.chmod(root/'.database-partial',0o600)
PY
pending="$WORK/spool/release-pending-aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee.json"
python3 - "$pending" <<'PY'
import json,os,sys
value={"format":1,"phase":"pending","authorization":{"source":{"export_id":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"}}}
with open(sys.argv[1],"w") as output: json.dump(value,output,sort_keys=True,separators=(",",":")); output.write("\n")
os.chmod(sys.argv[1],0o600)
PY
: > "$WORK/docker.log"
if run_export export >/dev/null 2>&1; then echo 'export ignored incomplete release' >&2; exit 1; fi
[ ! -s "$WORK/docker.log" ]
run_export reconcile >/dev/null
grep -q reconcile-postgres-exporter "$WORK/docker.log"
python3 - "$pending" "$WORK/spool/released-aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee.json" <<'PY'
import json,os,sys
source,target=sys.argv[1:]; value=json.load(open(source)); value["phase"]="released"
with open(target,"w") as output: json.dump(value,output,sort_keys=True,separators=(",",":")); output.write("\n")
os.chmod(target,0o600); os.unlink(source)
PY
: > "$WORK/docker.log"
if SIDEYARD_TEST_EXPORT_FAIL=1 run_export export >/dev/null 2>&1; then echo 'synthetic exporter failure succeeded' >&2; exit 1; fi
[ "$(grep -c export-postgres-logical "$WORK/docker.log")" = 1 ]
[ "$(grep -c reconcile-postgres-exporter "$WORK/docker.log")" = 2 ]
[ ! -e "$WORK/spool/.database-secret.env" ]
[ ! -e "$WORK/spool/operator-journal.json.new" ]
: > "$WORK/docker.log"
run_export export >/dev/null
[ ! -e "$WORK/spool/.database-partial" ]
[ "$(grep -c reconcile-postgres-exporter "$WORK/docker.log")" = 1 ]
[ "$(grep -c export-postgres-logical "$WORK/docker.log")" = 1 ]

printf '{bad json\n' > "$WORK/spool/operator-journal.json"
chmod 600 "$WORK/spool/operator-journal.json"
: > "$WORK/docker.log"
if run_export export >/dev/null 2>&1; then echo 'corrupt journal was accepted' >&2; exit 1; fi
grep -q reconcile-postgres-exporter "$WORK/docker.log"
grep -q '{bad json' "$WORK/spool/operator-journal.json"

grep -q 'command_timeout: 25m' "$ROOT/.github/workflows/staging-postgres-exporter.yml"
grep -A2 -q "name: Load tracked non-secret Azure storage identity.*inputs.action == 'export'" < <(tr '\n' ' ' < "$ROOT/.github/workflows/staging-postgres-exporter.yml")

echo 'Logical PostgreSQL export supervisor checks passed'
