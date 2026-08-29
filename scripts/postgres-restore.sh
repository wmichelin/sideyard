#!/usr/bin/env bash
# Restore an encrypted logical backup into an explicitly selected Postgres
# container. This is a rehearsal/operator command, never a deployment command.
set -euo pipefail

: "${CLUBDUE_RESTORE_DB_CONTAINER:?CLUBDUE_RESTORE_DB_CONTAINER required}"
: "${CLUBDUE_BACKUP_PASSPHRASE:?CLUBDUE_BACKUP_PASSPHRASE required}"

backup_file="${1:?usage: scripts/postgres-restore.sh /path/to/backup.sql.enc}"
CLUBDUE_RESTORE_DB_NAME="${CLUBDUE_RESTORE_DB_NAME:-clubdue}"
CLUBDUE_RESTORE_DB_USER="${CLUBDUE_RESTORE_DB_USER:-clubdue}"

if [ ! -f "$backup_file" ]; then
  echo "backup file does not exist" >&2
  exit 64
fi

openssl enc -d -aes-256-cbc -pbkdf2 -pass env:CLUBDUE_BACKUP_PASSPHRASE -in "$backup_file" \
  | docker exec -i "$CLUBDUE_RESTORE_DB_CONTAINER" \
    psql --set ON_ERROR_STOP=1 --username="$CLUBDUE_RESTORE_DB_USER" --dbname="$CLUBDUE_RESTORE_DB_NAME" >/dev/null

echo "backup restore completed"
