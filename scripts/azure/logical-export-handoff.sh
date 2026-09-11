#!/usr/bin/env bash
# Stream the exact staging package under the shared database-operation lock.
# Host release remains unavailable until exact Azure completion is authenticated.
set -euo pipefail

environment="${1:-}"
action="${2:-}"
[ "$environment" = staging ] || { echo 'Logical export handoff is staging-only' >&2; exit 64; }
[ "$action" = claim ] || { echo 'usage: logical-export-handoff.sh staging claim' >&2; exit 64; }
if [ -z "${SIDEYARD_EXPORTER_TEST_MODE:-}" ]; then
  [ "$(id -u)" = 0 ] || { echo 'Logical export handoff requires root' >&2; exit 77; }
fi

SIDEYARD_LOCK_ROOT="${SIDEYARD_LOCK_ROOT:-/run/lock/sideyard}"
if [ ! -e "$SIDEYARD_LOCK_ROOT" ]; then mkdir -m 700 "$SIDEYARD_LOCK_ROOT"; fi
[ -d "$SIDEYARD_LOCK_ROOT" ] && [ ! -L "$SIDEYARD_LOCK_ROOT" ] && [ "$(stat -c '%a' "$SIDEYARD_LOCK_ROOT")" = 700 ] || { echo 'Sideyard lock root is unsafe' >&2; exit 77; }
if [ -z "${SIDEYARD_EXPORTER_TEST_MODE:-}" ]; then
  [ "$(stat -c '%u:%g' "$SIDEYARD_LOCK_ROOT")" = 0:0 ] || { echo 'Sideyard lock root must be root-owned' >&2; exit 77; }
fi
exec 9>"${SIDEYARD_LOCK_ROOT}/sideyard-staging-database-window.lock"
flock -w 120 9 || { echo 'another staging database or deployment operation is active' >&2; exit 75; }

spool="${SIDEYARD_EXPORTER_SPOOL:-/var/lib/sideyard-logical-export}"
journal="${spool}/operator-journal.json"
if [ -z "${SIDEYARD_EXPORTER_TEST_MODE:-}" ] && [ "$spool" != /var/lib/sideyard-logical-export ]; then
  echo 'Logical export handoff target is invalid' >&2
  exit 64
fi
[ -d "$spool" ] && [ ! -L "$spool" ] && [ "$(stat -c '%a' "$spool")" = 700 ] || { echo 'Logical export spool is unsafe' >&2; exit 77; }
if [ -z "${SIDEYARD_EXPORTER_TEST_MODE:-}" ]; then
  [ "$(stat -c '%u:%g' "$spool")" = 0:0 ] || { echo 'Logical export spool must be root-owned' >&2; exit 77; }
fi

python3 - "$spool" "$journal" <<'PY'
import hashlib,json,os,re,stat,sys
from pathlib import Path

root,journal_path=map(Path,sys.argv[1:])
if not journal_path.exists(): raise SystemExit("logical export journal is missing")
info=journal_path.lstat()
if not stat.S_ISREG(info.st_mode) or info.st_nlink!=1 or info.st_uid!=os.geteuid() or stat.S_IMODE(info.st_mode)!=0o600: raise SystemExit("logical export journal is unsafe")
raw=journal_path.read_bytes()
try: journal=json.loads(raw)
except json.JSONDecodeError: raise SystemExit("logical export journal is invalid") from None
fields={"format","phase","environment","generation","revision","image","attempt","path","package_sha256","package_bytes","export_id","root_sha256"}
uuid=r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}"; sha=r"[0-9a-f]{64}"
patterns={"generation":uuid,"revision":r"[0-9a-f]{40}","image":r"ghcr\.io/wmichelin/clubdue@sha256:"+sha,"attempt":uuid,"export_id":uuid,"package_sha256":sha,"root_sha256":sha}
canonical=json.dumps(journal,sort_keys=True,separators=(",",":")).encode()+b"\n"
if not isinstance(journal,dict) or set(journal)!=fields or raw!=canonical or journal.get("format")!=1 or journal.get("phase")!="package_ready" or journal.get("environment")!="staging": raise SystemExit("logical export is not ready for handoff")
if any(not isinstance(journal.get(k),str) or not re.fullmatch(v,journal[k]) for k,v in patterns.items()): raise SystemExit("logical export journal identity is invalid")
if type(journal.get("package_bytes")) is not int or not 1<=journal["package_bytes"]<=250*1024*1024: raise SystemExit("logical export journal size is invalid")
expected=f"/var/lib/sideyard-logical-export/recovery-{journal['export_id']}.tar"
if journal.get("path")!=expected: raise SystemExit("logical export journal path is invalid")
package=root/Path(expected).name
descriptor=os.open(package,os.O_RDONLY|getattr(os,"O_NOFOLLOW",0))
with os.fdopen(descriptor,"rb") as source:
    info=os.fstat(source.fileno())
    if not stat.S_ISREG(info.st_mode) or info.st_nlink!=1 or info.st_uid!=os.geteuid() or stat.S_IMODE(info.st_mode)!=0o600 or info.st_size!=journal["package_bytes"]: raise SystemExit("logical export package is unsafe")
    digest=hashlib.sha256()
    for block in iter(lambda:source.read(1024*1024),b""): digest.update(block)
    if digest.hexdigest()!=journal["package_sha256"]: raise SystemExit("logical export package digest changed")
    sys.stdout.buffer.write(canonical); source.seek(0)
    for block in iter(lambda:source.read(1024*1024),b""): sys.stdout.buffer.write(block)
    sys.stdout.buffer.flush()
PY
