#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
mkdir -m 700 "$WORK/lock" "$WORK/spool"
generation=123e4567-e89b-42d3-a456-426614174000
attempt=223e4567-e89b-42d3-a456-426614174000
export_id=323e4567-e89b-42d3-a456-426614174000
revision="$(printf 'a%.0s' {1..40})"; image="ghcr.io/wmichelin/clubdue@sha256:$(printf 'b%.0s' {1..64})"
package="$WORK/spool/recovery-${export_id}.tar"; printf 'verified package bytes\n' > "$package"; chmod 600 "$package"
package_sha="$(sha256sum "$package" | cut -d' ' -f1)"; package_bytes="$(stat -c %s "$package")"; root_sha="$(printf 'c%.0s' {1..64})"
python3 - "$WORK/spool/operator-journal.json" "$generation" "$revision" "$image" "$attempt" "$export_id" "$package_sha" "$package_bytes" "$root_sha" <<'PY'
import json,os,sys
path,generation,revision,image,attempt,export_id,digest,size,root=sys.argv[1:]
value={"format":1,"phase":"package_ready","environment":"staging","generation":generation,"revision":revision,"image":image,"attempt":attempt,"path":f"/var/lib/sideyard-logical-export/recovery-{export_id}.tar","package_sha256":digest,"package_bytes":int(size),"export_id":export_id,"root_sha256":root}
with open(path,"w") as output: json.dump(value,output,sort_keys=True,separators=(",",":")); output.write("\n")
os.chmod(path,0o600)
PY
run_claim() { SIDEYARD_EXPORTER_TEST_MODE=1 SIDEYARD_EXPORTER_SPOOL="$WORK/spool" SIDEYARD_LOCK_ROOT="$WORK/lock" bash "$ROOT/scripts/azure/logical-export-handoff.sh" staging claim; }
run_claim > "$WORK/claim"
python3 - "$WORK/claim" "$package_bytes" "$package_sha" <<'PY'
import hashlib,json,sys
raw=open(sys.argv[1],"rb").read(); header,package=raw.split(b"\n",1); journal=json.loads(header)
assert len(package)==int(sys.argv[2]); assert hashlib.sha256(package).hexdigest()==sys.argv[3]; assert journal["package_sha256"]==sys.argv[3]
PY
ln "$package" "$WORK/spool/package-hardlink"
if run_claim >/dev/null 2>&1; then echo 'hard-linked package was accepted' >&2; exit 1; fi
rm "$WORK/spool/package-hardlink"
chmod 644 "$package"
if run_claim >/dev/null 2>&1; then echo 'permissive package was accepted' >&2; exit 1; fi
chmod 600 "$package"
if bash "$ROOT/scripts/azure/logical-export-handoff.sh" staging release >/dev/null 2>&1; then echo 'unimplemented release action was accepted' >&2; exit 1; fi
[ -f "$package" ] && [ -f "$WORK/spool/operator-journal.json" ]
echo 'Logical PostgreSQL export handoff checks passed'
