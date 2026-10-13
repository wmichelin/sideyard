#!/usr/bin/env bash
# Restore an encrypted logical backup into an explicitly selected Postgres
# container. This is a rehearsal/operator command, never a deployment command.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

: "${CLUBDUE_RESTORE_DB_CONTAINER:?CLUBDUE_RESTORE_DB_CONTAINER required}"
: "${CLUBDUE_BACKUP_PASSPHRASE:?CLUBDUE_BACKUP_PASSPHRASE required}"
: "${CLUBDUE_BACKUP_HMAC_KEY:?CLUBDUE_BACKUP_HMAC_KEY required}"

legacy_recovery=0
if [ "${1:-}" = "--legacy-unauthenticated" ]; then
  legacy_recovery=1
  shift
  case "$CLUBDUE_RESTORE_DB_CONTAINER" in
    sideyard-legacy-recovery-*) ;;
    *) echo "legacy recovery requires an isolated sideyard-legacy-recovery-* container" >&2; exit 64 ;;
  esac
fi
backup_file="${1:?usage: scripts/postgres-restore.sh [--legacy-unauthenticated] /path/to/backup.sql.enc}"
CLUBDUE_RESTORE_DB_NAME="${CLUBDUE_RESTORE_DB_NAME:-clubdue}"
CLUBDUE_RESTORE_DB_USER="${CLUBDUE_RESTORE_DB_USER:-clubdue}"

if [ ! -f "$backup_file" ]; then
  echo "backup file does not exist" >&2
  exit 64
fi

mac_file="${backup_file}.mac"
if [ ! -f "$mac_file" ]; then
  echo "backup authentication sidecar does not exist" >&2
  exit 64
fi

# Reject wrong keys, tampering and legacy sidecars before decryption/DB access.
if [ "$legacy_recovery" = "1" ]; then
  python3 "$SCRIPT_DIR/backup-auth.py" verify-legacy "$backup_file"
  # Unauthenticated legacy SQL must never reach a container with host/network
  # access or durable mounts, even if its name matches the recovery prefix.
  docker inspect "$CLUBDUE_RESTORE_DB_CONTAINER" | python3 -c '
import json, sys
items = json.load(sys.stdin)
if len(items) != 1:
    sys.exit("legacy recovery requires one isolated container")
c = items[0]
h = c.get("HostConfig", {})
safe = (h.get("NetworkMode") == "none" and not h.get("Privileged")
        and not h.get("Binds") and not h.get("Devices") and not h.get("CapAdd")
        and h.get("PidMode") != "host" and h.get("IpcMode") != "host"
        and all(m.get("Type") == "tmpfs" for m in c.get("Mounts", [])))
if not safe:
    sys.exit("legacy recovery requires network=none, no privileges/devices and tmpfs-only mounts")
'
else
  python3 "$SCRIPT_DIR/backup-auth.py" verify "$backup_file"
fi

# Finish decryption before opening the target database. A wrong passphrase can
# otherwise stream partial SQL to psql before OpenSSL rejects its final block.
# TemporaryFile is owner-only and unlinked; no plaintext pathname is published.
python3 - "$backup_file" "$CLUBDUE_RESTORE_DB_CONTAINER" "$CLUBDUE_RESTORE_DB_USER" "$CLUBDUE_RESTORE_DB_NAME" <<'PY'
import subprocess
import sys
import os

source, container, user, database = sys.argv[1:]
with os.fdopen(os.memfd_create("sideyard-restore", flags=os.MFD_CLOEXEC), "w+b", buffering=0) as plaintext:
    result = subprocess.run(
        ["openssl", "enc", "-d", "-aes-256-cbc", "-pbkdf2", "-pass",
         "env:CLUBDUE_BACKUP_PASSPHRASE", "-in", source],
        stdout=plaintext, stderr=subprocess.DEVNULL,
    )
    if result.returncode:
        sys.exit("backup decryption failed; target database was not accessed")
    plaintext.seek(0)
    if not plaintext.read(4096).startswith(b"--\n-- PostgreSQL database dump\n--\n"):
        sys.exit("backup plaintext is not a PostgreSQL dump; target database was not accessed")
    plaintext.seek(0)
    result = subprocess.run(
        ["docker", "exec", "-i", container, "psql", "--set", "ON_ERROR_STOP=1",
         "--username=" + user, "--dbname=" + database],
        stdin=plaintext, stdout=subprocess.DEVNULL,
    )
    sys.exit(result.returncode)
PY

echo "backup restore completed"
