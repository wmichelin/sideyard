#!/usr/bin/env bash
# Restore an encrypted logical backup into an explicitly selected Postgres
# container. This is a rehearsal/operator command, never a deployment command.
set -euo pipefail

: "${CLUBDUE_RESTORE_DB_CONTAINER:?CLUBDUE_RESTORE_DB_CONTAINER required}"
: "${CLUBDUE_BACKUP_PASSPHRASE:?CLUBDUE_BACKUP_PASSPHRASE required}"
: "${CLUBDUE_BACKUP_HMAC_KEY:?CLUBDUE_BACKUP_HMAC_KEY required}"

backup_file="${1:?usage: scripts/postgres-restore.sh /path/to/backup.sql.enc}"
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

# SHA-256 HMAC output must be exactly 32 bytes. Reject malformed sidecars
# before opening a database connection or attempting decryption.
if [ "$(wc -c < "$mac_file")" -ne 32 ]; then
  echo "backup authentication sidecar has an invalid length" >&2
  exit 64
fi

umask 077
computed_mac_file="$(mktemp "${TMPDIR:-/tmp}/clubdue-backup-mac.XXXXXX")"
cleanup() {
  rm -f "$computed_mac_file"
}
trap cleanup EXIT

# See postgres-backup.sh for why the HMAC key is supplied through fd 3 rather
# than a command-line option. cmp performs a complete byte-for-byte comparison
# and no restore command runs unless the computed MAC exactly matches.
openssl dgst -sha256 -mac HMAC -macopt key:file:/dev/fd/3 -binary \
  -out "$computed_mac_file" "$backup_file" 3< <(printf '%s' "$CLUBDUE_BACKUP_HMAC_KEY")
if ! cmp -s "$mac_file" "$computed_mac_file"; then
  echo "backup authentication failed" >&2
  exit 65
fi

openssl enc -d -aes-256-cbc -pbkdf2 -pass env:CLUBDUE_BACKUP_PASSPHRASE -in "$backup_file" \
  | docker exec -i "$CLUBDUE_RESTORE_DB_CONTAINER" \
    psql --set ON_ERROR_STOP=1 --username="$CLUBDUE_RESTORE_DB_USER" --dbname="$CLUBDUE_RESTORE_DB_NAME" >/dev/null

echo "backup restore completed"
