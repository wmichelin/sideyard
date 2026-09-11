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
release_request() { python3 - "$WORK/spool/operator-journal.json" <<'PY'
import base64,hashlib,json,sys,uuid
source=json.load(open(sys.argv[1])); completion={"key":"logical-completions/staging/123e4567-e89b-42d3-a456-426614174000/20260911T000000Z-323e4567-e89b-42d3-a456-426614174000/423e4567-e89b-42d3-a456-426614174000/completion.json","version":"2026-09-11T00:00:00.0000000Z","sha256":"d"*64,"size":2048,"stored_at":"Fri, 11 Sep 2026 00:00:00 GMT"}
canonical=lambda value:json.dumps(value,sort_keys=True,separators=(",",":")).encode()+b"\n"
value={"format":1,"environment":"staging","release_id":str(uuid.uuid4()),"target":"pantry-droplet","source_journal_sha256":hashlib.sha256(canonical(source)).hexdigest(),"receipt_sha256":"e"*64,"completion":completion,"source":source}
print(base64.urlsafe_b64encode(canonical(value)).decode())
PY
}
run_release() { SIDEYARD_EXPORTER_TEST_MODE=1 SIDEYARD_EXPORTER_SPOOL="$WORK/spool" SIDEYARD_LOCK_ROOT="$WORK/lock" SIDEYARD_RELEASE_FAILPOINT="${1:-}" bash "$ROOT/scripts/azure/logical-export-handoff.sh" staging release "$request"; }
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
if bash "$ROOT/scripts/azure/logical-export-handoff.sh" staging release >/dev/null 2>&1; then echo 'unauthorized release action was accepted' >&2; exit 1; fi
[ -f "$package" ] && [ -f "$WORK/spool/operator-journal.json" ]
cp "$package" "$WORK/original-package"; cp "$WORK/spool/operator-journal.json" "$WORK/original-journal"
restore_source() {
  rm -f "$WORK/spool/release-pending-${export_id}.json" "$WORK/spool/released-${export_id}.json" "$WORK/spool/operator-journal.json" "$package"
  cp "$WORK/original-package" "$package"; cp "$WORK/original-journal" "$WORK/spool/operator-journal.json"
  chmod 600 "$package" "$WORK/spool/operator-journal.json"
}
for failpoint in after-pending after-package-delete after-journal-delete after-tombstone; do
  restore_source; request="$(release_request)"
  if run_release "$failpoint" >/dev/null 2>&1; then echo "release failpoint ${failpoint} was ignored" >&2; exit 1; fi
  release_result="$(run_release)"
  [ ! -e "$package" ] && [ ! -e "$WORK/spool/operator-journal.json" ] && [ ! -e "$WORK/spool/release-pending-${export_id}.json" ] && [ -f "$WORK/spool/released-${export_id}.json" ]
done
python3 - "$release_result" "$export_id" <<'PY'
import json,sys
value=json.loads(sys.argv[1]); assert value["phase"]=="released" and value["export_id"]==sys.argv[2]
PY
# Lost acknowledgement converges from the tombstone without inspecting or
# deleting a newer active journal.
new_export=523e4567-e89b-42d3-a456-426614174000
cp "$WORK/original-package" "$WORK/spool/recovery-${new_export}.tar"; chmod 600 "$WORK/spool/recovery-${new_export}.tar"
python3 - "$WORK/original-journal" "$WORK/spool/operator-journal.json" "$new_export" <<'PY'
import json,os,sys
source,target,export_id=sys.argv[1:]; value=json.load(open(source)); value["export_id"]=export_id; value["path"]=f"/var/lib/sideyard-logical-export/recovery-{export_id}.tar"
with open(target,"w") as output: json.dump(value,output,sort_keys=True,separators=(",",":")); output.write("\n")
os.chmod(target,0o600)
PY
[ "$(run_release)" = "$release_result" ]
[ -f "$WORK/spool/operator-journal.json" ] && [ -f "$WORK/spool/recovery-${new_export}.tar" ]

# A dangling pending path is a conflict, never absence that may be overwritten.
rm -f "$WORK/spool/released-${export_id}.json" "$WORK/spool/operator-journal.json" "$WORK/spool/recovery-${new_export}.tar"; restore_source; request="$(release_request)"
ln -s "$WORK/missing" "$WORK/spool/release-pending-${export_id}.json"
if run_release >/dev/null 2>&1; then echo 'dangling release state was accepted' >&2; exit 1; fi
[ -f "$package" ] && [ -f "$WORK/spool/operator-journal.json" ] && [ -L "$WORK/spool/release-pending-${export_id}.json" ]
echo 'Logical PostgreSQL export handoff checks passed'
