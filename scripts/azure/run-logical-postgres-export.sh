#!/usr/bin/env bash
# Create one verified plaintext staging recovery package on the application host.
# Azure credentials and encryption keys deliberately remain off this host.
set -euo pipefail

environment="${1:-}"
action="${2:-export}"
[ "$environment" = staging ] || { echo 'Logical PostgreSQL export is staging-only' >&2; exit 64; }
case "$action" in export|reconcile) ;; *) echo 'usage: run-logical-postgres-export.sh staging export|reconcile' >&2; exit 64 ;; esac
if [ -z "${SIDEYARD_EXPORTER_TEST_MODE:-}" ]; then
  [ "$(id -u)" = 0 ] || { echo 'Logical PostgreSQL export requires root' >&2; exit 77; }
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
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

app_container="${SIDEYARD_EXPORTER_APP_CONTAINER:-clubdue-staging-current}"
db_container="${SIDEYARD_EXPORTER_DB_CONTAINER:-clubdue-staging-db}"
network="${SIDEYARD_EXPORTER_NETWORK:-clubdue-staging-net}"
spool="${SIDEYARD_EXPORTER_SPOOL:-/var/lib/sideyard-logical-export}"
journal="${spool}/operator-journal.json"
result_file="${spool}/.operator-result.json"
secret_mount="${spool}/.database-secret.env"
export_container="sideyard-staging-logical-export"
database_env="${SIDEYARD_EXPORTER_DATABASE_ENV:-/opt/clubdue/staging-db.env}"
docker_user_args=()
export_test_args=()
if [ -z "${SIDEYARD_EXPORTER_TEST_MODE:-}" ]; then
  [ "$app_container" = clubdue-staging-current ] && [ "$db_container" = clubdue-staging-db ] &&
    [ "$network" = clubdue-staging-net ] && [ "$spool" = /var/lib/sideyard-logical-export ] || {
      echo 'Logical PostgreSQL export target is invalid' >&2; exit 64;
    }
else
  docker_user_args=(--user "$(id -u):$(id -g)")
  if [ -n "${SIDEYARD_EXPORTER_FAILPOINT:-}" ]; then
    export_test_args=(-e SIDEYARD_EXPORTER_REHEARSAL=1 -e "SIDEYARD_EXPORTER_FAILPOINT=${SIDEYARD_EXPORTER_FAILPOINT}")
  fi
fi

[ -f "$database_env" ] && [ ! -L "$database_env" ] || { echo 'staging database environment file is missing or unsafe' >&2; exit 66; }
env_mode="$(stat -c '%a' "$database_env")"
env_owner="$(stat -c '%u:%g' "$database_env")"
[ $((8#$env_mode & 077)) -eq 0 ] || { echo 'staging database environment file must be owner-only' >&2; exit 77; }
if [ -z "${SIDEYARD_EXPORTER_TEST_MODE:-}" ]; then
  [ "$env_owner" = 0:0 ] || { echo 'staging database environment file must be root-owned' >&2; exit 77; }
fi
if [ ! -e "$spool" ]; then mkdir -m 700 "$spool"; fi
[ -d "$spool" ] && [ ! -L "$spool" ] && [ "$(stat -c '%a' "$spool")" = 700 ] || {
  echo 'Logical export spool must be a protected directory' >&2; exit 77;
}
if [ -z "${SIDEYARD_EXPORTER_TEST_MODE:-}" ]; then
  [ "$(stat -c '%u:%g' "$spool")" = 0:0 ] || { echo 'Logical export spool must be root-owned' >&2; exit 77; }
fi

python3 - "$database_env" "$secret_mount" <<'PY'
import os,stat,sys
source,target=sys.argv[1:]
info=os.lstat(source)
if not stat.S_ISREG(info.st_mode) or info.st_mode&0o077 or info.st_uid!=os.geteuid(): raise SystemExit("staging database environment file is unsafe")
values={}
with open(source,encoding="utf-8") as handle:
    for line in handle:
        key,separator,value=line.rstrip("\n").partition("=")
        if not separator or key in values: raise SystemExit("staging database environment file is invalid")
        values[key]=value
if set(values)!={"POSTGRES_PASSWORD","SESSION_SECRET"} or len(values["POSTGRES_PASSWORD"])!=48 or any(c not in "0123456789abcdef" for c in values["POSTGRES_PASSWORD"]): raise SystemExit("staging database environment file is invalid")
try:
    old=os.lstat(target)
    if not stat.S_ISREG(old.st_mode) or old.st_mode&0o077 or old.st_uid!=os.geteuid(): raise SystemExit("stale database secret mount is unsafe")
    os.unlink(target)
except FileNotFoundError: pass
descriptor=os.open(target,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
with os.fdopen(descriptor,"w") as output:
    output.write("POSTGRES_PASSWORD="+values["POSTGRES_PASSWORD"]+"\n"); output.flush(); os.fsync(output.fileno())
PY
remove_secret() {
  python3 - "$secret_mount" <<'PY'
from pathlib import Path
import sys
Path(sys.argv[1]).unlink(missing_ok=True)
PY
}
trap remove_secret EXIT

journal_value() {
  python3 - "$journal" "$1" <<'PY'
import json,sys
with open(sys.argv[1], encoding="utf-8") as source:
    value=json.load(source)
field=value.get(sys.argv[2])
if not isinstance(field, str): raise SystemExit(1)
print(field)
PY
}

validate_journal() {
  [ -f "$journal" ] && [ ! -L "$journal" ] && [ "$(stat -c '%a' "$journal")" = 600 ] || return 1
  python3 - "$journal" <<'PY'
import json,re,sys
with open(sys.argv[1], encoding="utf-8") as source: value=json.load(source)
required={"format","phase","environment","generation","revision","image","attempt"}
if set(value) < required or value["format"] != 1 or value["environment"] != "staging": raise SystemExit(1)
if value["phase"] not in ("pre_activation","activating","activated","package_ready"): raise SystemExit(1)
uuid=r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}"
patterns={"generation":uuid,"revision":r"[0-9a-f]{40}","image":r"ghcr.io/wmichelin/clubdue@sha256:[0-9a-f]{64}","attempt":uuid}
if any(not isinstance(value.get(k),str) or not re.fullmatch(p,value[k]) for k,p in patterns.items()): raise SystemExit(1)
if value["phase"] == "pre_activation" and set(value) != required: raise SystemExit(1)
if value["phase"] in ("activating","activated"):
    if set(value) != required | {"server_deadline"}: raise SystemExit(1)
    if not isinstance(value["server_deadline"],str) or not re.fullmatch(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z",value["server_deadline"]): raise SystemExit(1)
if value["phase"] == "package_ready":
    if set(value) != required | {"path","package_sha256","package_bytes","export_id","root_sha256"}: raise SystemExit(1)
    if not re.fullmatch(r"/var/lib/sideyard-logical-export/recovery-[0-9a-f-]{36}\.tar",value["path"]): raise SystemExit(1)
    if not re.fullmatch(r"[0-9a-f]{64}",value["package_sha256"]) or not re.fullmatch(r"[0-9a-f]{64}",value["root_sha256"]): raise SystemExit(1)
    if type(value["package_bytes"]) is not int or value["package_bytes"] < 1: raise SystemExit(1)
PY
}

run_restriction() {
  local image="$1"
  docker rm -f "$export_container" >/dev/null 2>&1 || true
  docker rm -f "${export_container}-reconcile" >/dev/null 2>&1 || true
  docker run --rm --name "${export_container}-reconcile" --read-only --cap-drop ALL --security-opt no-new-privileges \
    "${docker_user_args[@]}" \
    --network "$network" -e APP_ENV=staging \
    -e SIDEYARD_DATABASE_PASSWORD_FILE=/run/secrets/staging-db.env \
    -v "$secret_mount:/run/secrets/staging-db.env:ro" \
    "$image" reconcile-postgres-exporter 9>&-
}

remove_journal_temporary() {
  python3 - "${journal}.new" "$spool" <<'PY'
import os,stat,sys
target,root=sys.argv[1:]
try: info=os.lstat(target)
except FileNotFoundError: raise SystemExit(0)
if not stat.S_ISREG(info.st_mode) or info.st_nlink!=1 or info.st_mode&0o777!=0o600 or info.st_uid!=os.geteuid(): raise SystemExit("unsafe logical export journal temporary")
os.unlink(target); descriptor=os.open(root,os.O_RDONLY); os.fsync(descriptor); os.close(descriptor)
PY
}

remove_incomplete_files() {
  python3 - "$spool" <<'PY'
import os,stat,sys
from pathlib import Path
root=Path(sys.argv[1])
for child in root.iterdir():
    if child.name in ("operator-journal.json",".database-secret.env"): continue
    if not (child.name.startswith(".database-") or child.name.startswith(".recovery-") or child.name in (".operator-result.json","operator-journal.json.new") or (child.name.startswith("recovery-") and child.name.endswith(".tar"))):
        raise SystemExit("unexpected logical export spool entry")
    info=child.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid(): raise SystemExit("unsafe logical export spool entry")
    child.unlink()
PY
}

if [ -e "${journal}.new" ] && [ ! -e "$journal" ]; then
  recovery_image="$(docker inspect -f '{{.Config.Image}}' "$app_container")"
  [[ "$recovery_image" =~ ^ghcr.io/wmichelin/clubdue@sha256:[0-9a-f]{64}$ ]] || { echo 'Stale journal transition has no immutable recovery image' >&2; exit 65; }
  run_restriction "$recovery_image" >/dev/null
  remove_journal_temporary
fi

if [ -e "$journal" ]; then
  if ! validate_journal; then
    recovery_image="$(docker inspect -f '{{.Config.Image}}' "$app_container")"
    [[ "$recovery_image" =~ ^ghcr.io/wmichelin/clubdue@sha256:[0-9a-f]{64}$ ]] || { echo 'Logical export journal is corrupt and no immutable recovery image is available' >&2; exit 65; }
    run_restriction "$recovery_image" >/dev/null
    echo 'Logical export journal is unsafe or corrupt; exporter authority was removed but manual spool review is required' >&2
    exit 65
  fi
  prior_phase="$(journal_value phase)"
  prior_image="$(journal_value image)"
  run_restriction "$prior_image" >/dev/null
  if [ "$prior_phase" = package_ready ]; then
    echo 'A verified logical package is awaiting off-host handoff' >&2
    exit 75
  fi
  remove_incomplete_files
  python3 - "$journal" "$spool" <<'PY'
import os,sys
os.unlink(sys.argv[1]); descriptor=os.open(sys.argv[2],os.O_RDONLY); os.fsync(descriptor); os.close(descriptor)
PY
fi

if [ "$action" = reconcile ]; then
  image="$(docker inspect -f '{{.Config.Image}}' "$app_container")"
  [[ "$image" =~ ^ghcr.io/wmichelin/clubdue@sha256:[0-9a-f]{64}$ ]] || { echo 'running staging image is not immutable' >&2; exit 65; }
  run_restriction "$image"
  exit 0
fi

load_sideyard_runtime_state staging
[ "$SIDEYARD_BACKEND" = postgres ] || { echo 'PostgreSQL is not the authoritative staging source' >&2; exit 65; }
verify_sideyard_current_container staging "$app_container"
[ "$(docker inspect -f '{{.State.Running}}' "$db_container" 2>/dev/null || true)" = true ] || { echo 'staging PostgreSQL container is not running' >&2; exit 69; }
revision="$(docker inspect -f '{{ index .Config.Labels "org.opencontainers.image.revision" }}' "$app_container")"
image="$(docker inspect -f '{{.Config.Image}}' "$app_container")"
[[ "$revision" =~ ^[0-9a-f]{40}$ ]] && [[ "$image" =~ ^ghcr.io/wmichelin/clubdue@sha256:[0-9a-f]{64}$ ]] || {
  echo 'running staging artifact identity is invalid' >&2; exit 65;
}
if [ -n "${SIDEYARD_EXPECTED_REVISION:-}" ] && [ "$revision" != "$SIDEYARD_EXPECTED_REVISION" ]; then
  echo 'running staging revision does not match the reviewed revision' >&2; exit 65
fi
subscription="${SIDEYARD_AZURE_SUBSCRIPTION_ID:-}"
photo_account="${SIDEYARD_PHOTO_ACCOUNT:-}"
[[ "$subscription" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$ ]] || { echo 'staging Azure subscription is invalid' >&2; exit 65; }
[[ "$photo_account" =~ ^sideyard[a-z0-9]{1,16}$ ]] || { echo 'staging photo account is invalid' >&2; exit 65; }
photo_resource="/subscriptions/${subscription}/resourceGroups/sideyard-staging/providers/Microsoft.Storage/storageAccounts/${photo_account}"
attempt="$(cat /proc/sys/kernel/random/uuid)"

python3 - "$journal" "$spool" "$SIDEYARD_GENERATION" "$revision" "$image" "$attempt" <<'PY'
import json,os,sys
target,root,generation,revision,image,attempt=sys.argv[1:]
temporary=target+".new"
value={"format":1,"phase":"pre_activation","environment":"staging","generation":generation,"revision":revision,"image":image,"attempt":attempt}
descriptor=os.open(temporary,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
with os.fdopen(descriptor,"w") as output:
    json.dump(value,output,sort_keys=True,separators=(",",":")); output.write("\n"); output.flush(); os.fsync(output.fileno())
os.replace(temporary,target); directory=os.open(root,os.O_RDONLY); os.fsync(directory); os.close(directory)
PY

operation_started=1
cleanup() {
  result=$?
  trap - EXIT
  docker rm -f "$export_container" >/dev/null 2>&1 || true
  if [ "$result" -ne 0 ] && [ "${operation_started:-0}" = 1 ]; then
    if ! run_restriction "$image" >/dev/null; then
      echo 'Logical export failed and exporter restriction also failed; run reconcile immediately' >&2
      result=70
    fi
  fi
  remove_secret
  exit "$result"
}
trap cleanup EXIT
docker run --rm --name "$export_container" --read-only --cap-drop ALL --security-opt no-new-privileges \
  "${docker_user_args[@]}" "${export_test_args[@]}" \
  --tmpfs /tmp:rw,noexec,nosuid,size=300m --network "$network" \
  -e APP_ENV=staging -e DATABASE_BACKEND=postgres -e "DATA_GENERATION=${SIDEYARD_GENERATION}" \
  -e SIDEYARD_DATABASE_PASSWORD_FILE=/run/secrets/staging-db.env \
  -e "SIDEYARD_SOURCE_REVISION=${revision}" -e "SIDEYARD_SOURCE_ARTIFACT=${image}" \
  -e "SIDEYARD_PHOTO_ACCOUNT=${photo_account}" -e "SIDEYARD_PHOTO_RESOURCE_ID=${photo_resource}" \
  -v "$secret_mount:/run/secrets/staging-db.env:ro" \
  -v "$spool:/var/lib/sideyard-logical-export" "$image" export-postgres-logical > "$result_file" 9>&-
chmod 600 "$result_file"

python3 - "$journal" "$result_file" "$spool" "$SIDEYARD_GENERATION" "$revision" "$image" "$attempt" <<'PY'
import hashlib,json,os,re,stat,sys
journal,result_path,root,generation,revision,image,attempt=sys.argv[1:]
with open(journal,encoding="utf-8") as source: prior=json.load(source)
if prior.get("phase")!="activated" or prior.get("generation")!=generation or prior.get("revision")!=revision or prior.get("image")!=image or prior.get("attempt")!=attempt or not isinstance(prior.get("server_deadline"),str): raise SystemExit("logical export activation journal mismatch")
with open(result_path,encoding="utf-8") as source: result=json.load(source)
required={"path","export_id","package_sha256","package_bytes","root_sha256"}
if set(result)!=required or not re.fullmatch(r"/var/lib/sideyard-logical-export/recovery-[0-9a-f-]{36}\.tar",result["path"]): raise SystemExit("invalid logical export result")
host_path=os.path.join(root,os.path.basename(result["path"]))
info=os.stat(host_path,follow_symlinks=False)
if not stat.S_ISREG(info.st_mode) or info.st_nlink!=1 or info.st_uid!=os.geteuid() or info.st_mode&0o777!=0o600 or info.st_size!=result["package_bytes"]: raise SystemExit("logical package file is unsafe")
digest=hashlib.sha256()
with open(host_path,"rb") as source:
    for block in iter(lambda:source.read(1024*1024),b""): digest.update(block)
if digest.hexdigest()!=result["package_sha256"]: raise SystemExit("logical package digest mismatch")
value={"format":1,"phase":"package_ready","environment":"staging","generation":generation,"revision":revision,"image":image,"attempt":attempt,**result}
temporary=journal+".new"; descriptor=os.open(temporary,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
with os.fdopen(descriptor,"w") as output:
    json.dump(value,output,sort_keys=True,separators=(",",":")); output.write("\n"); output.flush(); os.fsync(output.fileno())
os.replace(temporary,journal); os.unlink(result_path); directory=os.open(root,os.O_RDONLY); os.fsync(directory); os.close(directory)
print(json.dumps(result,sort_keys=True,separators=(",",":")))
PY
