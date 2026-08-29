#!/usr/bin/env bash
# Create an encrypted logical Postgres backup. This command is intended for a
# root-only production backup destination and is also exercised by the local
# production rehearsal. It never creates a database or prints credentials.
set -euo pipefail

: "${CLUBDUE_BACKUP_DB_CONTAINER:?CLUBDUE_BACKUP_DB_CONTAINER required}"
: "${CLUBDUE_BACKUP_DIR:?CLUBDUE_BACKUP_DIR required}"
: "${CLUBDUE_BACKUP_PASSPHRASE:?CLUBDUE_BACKUP_PASSPHRASE required}"

CLUBDUE_BACKUP_DB_NAME="${CLUBDUE_BACKUP_DB_NAME:-clubdue}"
CLUBDUE_BACKUP_DB_USER="${CLUBDUE_BACKUP_DB_USER:-clubdue}"
CLUBDUE_BACKUP_RETENTION_DAYS="${CLUBDUE_BACKUP_RETENTION_DAYS:-30}"

case "$CLUBDUE_BACKUP_RETENTION_DAYS" in
  ''|*[!0-9]*)
    echo "CLUBDUE_BACKUP_RETENTION_DAYS must be a non-negative integer" >&2
    exit 64
    ;;
esac

if [ "$CLUBDUE_BACKUP_DIR" = "/" ] || [ "$CLUBDUE_BACKUP_DIR" = "." ]; then
  echo "CLUBDUE_BACKUP_DIR must be a dedicated directory" >&2
  exit 64
fi

umask 077
mkdir -p "$CLUBDUE_BACKUP_DIR"
backup_file="${CLUBDUE_BACKUP_DIR}/clubdue-postgres-$(date -u +%Y%m%dT%H%M%SZ).sql.enc"

docker exec "$CLUBDUE_BACKUP_DB_CONTAINER" \
  pg_dump --username="$CLUBDUE_BACKUP_DB_USER" --dbname="$CLUBDUE_BACKUP_DB_NAME" --format=plain \
  | openssl enc -aes-256-cbc -salt -pbkdf2 -pass env:CLUBDUE_BACKUP_PASSPHRASE -out "$backup_file"

# Retention is limited to this script's explicit filename pattern and dedicated
# directory. A retention value of zero expires only backups older than today.
find "$CLUBDUE_BACKUP_DIR" -maxdepth 1 -type f -name 'clubdue-postgres-*.sql.enc' \
  -mtime "+${CLUBDUE_BACKUP_RETENTION_DAYS}" -delete

printf '%s\n' "$backup_file"
