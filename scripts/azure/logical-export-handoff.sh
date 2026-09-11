#!/usr/bin/env bash
# Stream or explicitly release one exact staging package under the shared lock.
set -euo pipefail

environment="${1:-}"; action="${2:-}"; request="${3:-}"
[ "$environment" = staging ] || { echo 'Logical export handoff is staging-only' >&2; exit 64; }
case "$action" in claim|release) ;; *) echo 'usage: logical-export-handoff.sh staging claim|release [authorization-base64]' >&2; exit 64 ;; esac
[ "$action" = release ] || [ -z "$request" ] || { echo 'claim does not accept a release request' >&2; exit 64; }
if [ -z "${SIDEYARD_EXPORTER_TEST_MODE:-}" ]; then [ "$(id -u)" = 0 ] || { echo 'Logical export handoff requires root' >&2; exit 77; }; fi

SIDEYARD_LOCK_ROOT="${SIDEYARD_LOCK_ROOT:-/run/sideyard-locks}"
if [ ! -e "$SIDEYARD_LOCK_ROOT" ]; then mkdir -m 700 "$SIDEYARD_LOCK_ROOT"; fi
[ -d "$SIDEYARD_LOCK_ROOT" ] && [ ! -L "$SIDEYARD_LOCK_ROOT" ] && [ "$(stat -c '%a' "$SIDEYARD_LOCK_ROOT")" = 700 ] || { echo 'Sideyard lock root is unsafe' >&2; exit 77; }
if [ -z "${SIDEYARD_EXPORTER_TEST_MODE:-}" ]; then [ "$(stat -c '%u:%g' "$SIDEYARD_LOCK_ROOT")" = 0:0 ] || { echo 'Sideyard lock root must be root-owned' >&2; exit 77; }; fi
exec 9>"${SIDEYARD_LOCK_ROOT}/sideyard-staging-database-window.lock"
flock -w 120 9 || { echo 'another staging database or deployment operation is active' >&2; exit 75; }

spool="${SIDEYARD_EXPORTER_SPOOL:-/var/lib/sideyard-logical-export}"; journal="${spool}/operator-journal.json"
if [ -z "${SIDEYARD_EXPORTER_TEST_MODE:-}" ] && [ "$spool" != /var/lib/sideyard-logical-export ]; then echo 'Logical export handoff target is invalid' >&2; exit 64; fi
[ -d "$spool" ] && [ ! -L "$spool" ] && [ "$(stat -c '%a' "$spool")" = 700 ] || { echo 'Logical export spool is unsafe' >&2; exit 77; }
if [ -z "${SIDEYARD_EXPORTER_TEST_MODE:-}" ]; then [ "$(stat -c '%u:%g' "$spool")" = 0:0 ] || { echo 'Logical export spool must be root-owned' >&2; exit 77; }; fi

# package_ready is written after deactivation. Repeat restriction immediately
# before deletion, while this command owns the same database-operation lock.
if [ "$action" = release ]; then
  release_export="$(python3 - "$request" <<'PY'
import base64,json,re,sys
try: value=json.loads(base64.urlsafe_b64decode(sys.argv[1].encode()))
except Exception: raise SystemExit("logical release authorization is invalid") from None
export_id=value.get("source",{}).get("export_id") if isinstance(value,dict) else None
if not isinstance(export_id,str) or not re.fullmatch(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}",export_id): raise SystemExit("logical release authorization is invalid")
print(export_id)
PY
)"
  resume_release=0
  [ -e "$spool/release-pending-${release_export}.json" ] || [ -L "$spool/release-pending-${release_export}.json" ] ||
    [ -e "$spool/released-${release_export}.json" ] || [ -L "$spool/released-${release_export}.json" ] || resume_release=1
  if [ "$resume_release" = 1 ] && [ -z "${SIDEYARD_EXPORTER_TEST_MODE:-}" ]; then
    state_file="${SIDEYARD_STATE_ROOT:-/var/lib/sideyard-operations}/staging-backend.env"
    [ -f "$state_file" ] && [ ! -L "$state_file" ] && [ "$(stat -c '%a' "$state_file")" = 600 ] && [ "$(stat -c '%u:%g' "$state_file")" = 0:0 ] || { echo 'staging backend state is missing or unsafe' >&2; exit 77; }
    [ "$(wc -l < "$state_file")" = 2 ] && [ "$(grep -c '^DATABASE_BACKEND=' "$state_file")" = 1 ] && [ "$(grep -c '^DATA_GENERATION=' "$state_file")" = 1 ] || { echo 'staging backend state is invalid' >&2; exit 65; }
    active_backend="$(sed -n 's/^DATABASE_BACKEND=//p' "$state_file")"; active_generation="$(sed -n 's/^DATA_GENERATION=//p' "$state_file")"
    [ "$active_backend" = postgres ] && [[ "$active_generation" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$ ]] || { echo 'PostgreSQL is not the authoritative staging source' >&2; exit 65; }
    [ "$(docker inspect -f '{{.State.Running}}' clubdue-staging-current 2>/dev/null || true)" = true ] &&
      [ "$(docker inspect -f '{{ index .Config.Labels "sideyard.backend" }}' clubdue-staging-current 2>/dev/null || true)" = postgres ] &&
      [ "$(docker inspect -f '{{ index .Config.Labels "sideyard.generation" }}' clubdue-staging-current 2>/dev/null || true)" = "$active_generation" ] || { echo 'staging application does not match authoritative PostgreSQL data' >&2; exit 69; }
    export SIDEYARD_ACTIVE_GENERATION="$active_generation"
    docker exec clubdue-staging-current /clubdue reconcile-postgres-exporter >/dev/null
  fi
fi

python3 - "$spool" "$journal" "$action" "$request" <<'PY'
import base64,hashlib,json,os,re,stat,sys
from pathlib import Path
root,journal_path=map(Path,sys.argv[1:3]); action,encoded=sys.argv[3:]
uuid=r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}"; sha=r"[0-9a-f]{64}"
fields={"format","phase","environment","generation","revision","image","attempt","path","package_sha256","package_bytes","export_id","root_sha256"}
def canonical(value): return json.dumps(value,sort_keys=True,separators=(",",":")).encode()+b"\n"
def present(path):
    try: path.lstat(); return True
    except FileNotFoundError: return False
def private_file(path):
    info=path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_nlink!=1 or info.st_uid!=os.geteuid() or stat.S_IMODE(info.st_mode)!=0o600: raise SystemExit("logical handoff state is unsafe")
    return info
def read_canonical(path):
    private_file(path); raw=path.read_bytes()
    try: value=json.loads(raw)
    except json.JSONDecodeError: raise SystemExit("logical handoff state is invalid") from None
    if raw!=canonical(value): raise SystemExit("logical handoff state is not canonical")
    return value,raw
def fsync_root():
    descriptor=os.open(root,os.O_RDONLY); os.fsync(descriptor); os.close(descriptor)
def persist(path,value):
    content=canonical(value); temporary=path.with_name("."+path.name+".new")
    try: descriptor=os.open(temporary,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
    except FileExistsError:
        private_file(temporary)
        if temporary.read_bytes()!=content: raise SystemExit("logical handoff transition is unsafe")
    else:
        with os.fdopen(descriptor,"wb") as output: output.write(content); output.flush(); os.fsync(output.fileno())
    os.replace(temporary,path); fsync_root()
def validate_source(value):
    patterns={"generation":uuid,"revision":r"[0-9a-f]{40}","image":r"ghcr\.io/wmichelin/clubdue@sha256:"+sha,"attempt":uuid,"export_id":uuid,"package_sha256":sha,"root_sha256":sha}
    if not isinstance(value,dict) or set(value)!=fields or value.get("format")!=1 or value.get("phase")!="package_ready" or value.get("environment")!="staging": raise SystemExit("logical export journal is invalid")
    if any(not isinstance(value.get(k),str) or not re.fullmatch(v,value[k]) for k,v in patterns.items()): raise SystemExit("logical export journal identity is invalid")
    if type(value.get("package_bytes")) is not int or not 1<=value["package_bytes"]<=250*1024*1024: raise SystemExit("logical export journal size is invalid")
    if value["path"]!=f"/var/lib/sideyard-logical-export/recovery-{value['export_id']}.tar": raise SystemExit("logical export journal path is invalid")
def open_package(source):
    package=root/Path(source["path"]).name; descriptor=os.open(package,os.O_RDONLY|getattr(os,"O_NOFOLLOW",0)); handle=os.fdopen(descriptor,"rb"); info=os.fstat(handle.fileno())
    if not stat.S_ISREG(info.st_mode) or info.st_nlink!=1 or info.st_uid!=os.geteuid() or stat.S_IMODE(info.st_mode)!=0o600 or info.st_size!=source["package_bytes"]: handle.close(); raise SystemExit("logical export package is unsafe")
    digest=hashlib.sha256()
    for block in iter(lambda:handle.read(1024*1024),b""): digest.update(block)
    if digest.hexdigest()!=source["package_sha256"]: handle.close(); raise SystemExit("logical export package digest changed")
    handle.seek(0); return package,handle,info

if action=="claim":
    if encoded or not present(journal_path): raise SystemExit("logical export is not claimable")
    journal,raw=read_canonical(journal_path); validate_source(journal); _,source,_=open_package(journal)
    with source:
        sys.stdout.buffer.write(raw)
        for block in iter(lambda:source.read(1024*1024),b""): sys.stdout.buffer.write(block)
        sys.stdout.buffer.flush()
    raise SystemExit(0)

if len(encoded)>32768 or not re.fullmatch(r"[A-Za-z0-9_-]+={0,2}",encoded): raise SystemExit("logical release authorization is invalid")
try: authorization_raw=base64.urlsafe_b64decode(encoded.encode()); authorization=json.loads(authorization_raw)
except Exception: raise SystemExit("logical release authorization is invalid") from None
authorization_fields={"format","environment","release_id","target","source_journal_sha256","receipt_sha256","completion","source"}
if authorization_raw!=canonical(authorization) or not isinstance(authorization,dict) or set(authorization)!=authorization_fields or authorization.get("format")!=1 or authorization.get("environment")!="staging" or authorization.get("target")!="pantry-droplet" or not re.fullmatch(uuid,str(authorization.get("release_id",""))) or not re.fullmatch(sha,str(authorization.get("receipt_sha256",""))): raise SystemExit("logical release authorization is invalid")
source=authorization["source"]; validate_source(source)
if os.environ.get("SIDEYARD_ACTIVE_GENERATION") and source["generation"]!=os.environ["SIDEYARD_ACTIVE_GENERATION"]: raise SystemExit("logical release source generation is no longer authoritative")
if authorization.get("source_journal_sha256")!=hashlib.sha256(canonical(source)).hexdigest(): raise SystemExit("logical release authorization source digest is invalid")
completion=authorization.get("completion")
completion_pattern=rf"logical-completions/staging/{source['generation']}/\d{{8}}T\d{{6}}Z-{source['export_id']}/{uuid}/completion\.json"
if not isinstance(completion,dict) or set(completion)!={"key","version","sha256","size","stored_at"} or not isinstance(completion["key"],str) or not re.fullmatch(completion_pattern,completion["key"]) or not completion["version"] or not re.fullmatch(sha,str(completion["sha256"])) or type(completion["size"]) is not int or not 1<=completion["size"]<=32768 or not completion["stored_at"]: raise SystemExit("logical release completion reference is invalid")

pending=root/f"release-pending-{source['export_id']}.json"; tombstone=root/f"released-{source['export_id']}.json"
pending_value={"format":1,"phase":"pending","authorization":authorization}; released_value={"format":1,"phase":"released","authorization":authorization}
if present(tombstone):
    existing,_=read_canonical(tombstone)
    if existing!=released_value: raise SystemExit("logical release tombstone conflicts with authorization")
    if present(pending):
        existing,_=read_canonical(pending)
        if existing!=pending_value: raise SystemExit("logical release pending state conflicts with tombstone")
        pending.unlink(); fsync_root()
    result={"environment":"staging","export_id":source["export_id"],"package_sha256":source["package_sha256"],"phase":"released","release_id":authorization["release_id"]}
    sys.stdout.buffer.write(canonical(result)); sys.stdout.buffer.flush(); raise SystemExit(0)
if present(pending):
    existing,_=read_canonical(pending)
    if existing!=pending_value: raise SystemExit("logical release pending state conflicts with authorization")
else:
    if not present(journal_path): raise SystemExit("logical export journal is missing before release intent")
    current,current_raw=read_canonical(journal_path)
    if current!=source or current_raw!=canonical(source): raise SystemExit("logical release authorization does not match the host export")
    _,handle,_=open_package(source); handle.close(); persist(pending,pending_value)
    if os.environ.get("SIDEYARD_EXPORTER_TEST_MODE") and os.environ.get("SIDEYARD_RELEASE_FAILPOINT")=="after-pending": raise SystemExit(75)
if present(journal_path):
    current,current_raw=read_canonical(journal_path)
    if current!=source or current_raw!=canonical(source): raise SystemExit("logical release authorization does not match the host export")
    package=root/Path(source["path"]).name
    if present(package):
        package,handle,opened=open_package(source); current_info=package.lstat()
        if (current_info.st_dev,current_info.st_ino)!=(opened.st_dev,opened.st_ino): handle.close(); raise SystemExit("logical release package identity changed")
        handle.close(); package.unlink(); fsync_root()
        if os.environ.get("SIDEYARD_EXPORTER_TEST_MODE") and os.environ.get("SIDEYARD_RELEASE_FAILPOINT")=="after-package-delete": raise SystemExit(75)
    journal_path.unlink(); fsync_root()
    if os.environ.get("SIDEYARD_EXPORTER_TEST_MODE") and os.environ.get("SIDEYARD_RELEASE_FAILPOINT")=="after-journal-delete": raise SystemExit(75)
elif present(root/Path(source["path"]).name): raise SystemExit("logical release package exists without its journal")
persist(tombstone,released_value)
if os.environ.get("SIDEYARD_EXPORTER_TEST_MODE") and os.environ.get("SIDEYARD_RELEASE_FAILPOINT")=="after-tombstone": raise SystemExit(75)
pending.unlink(); fsync_root()
result={"environment":"staging","export_id":source["export_id"],"package_sha256":source["package_sha256"],"phase":"released","release_id":authorization["release_id"]}
sys.stdout.buffer.write(canonical(result)); sys.stdout.buffer.flush()
PY
