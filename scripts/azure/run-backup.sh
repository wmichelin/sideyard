#!/usr/bin/env bash
# Run from a root-only environment file via the matching systemd service.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
environment="${1:?usage: run-backup.sh staging|production}"
case "$environment" in
  staging) export CLUBDUE_BACKUP_DB_CONTAINER=clubdue-staging-db ;;
  production) export CLUBDUE_BACKUP_DB_CONTAINER=clubdue-production-db ;;
  *) echo 'Unsupported Sideyard backup environment' >&2; exit 64 ;;
esac
source "$SCRIPT_DIR/runtime-state.sh"
umask 077
export CLUBDUE_BACKUP_DIR="/var/backups/sideyard-azure/${environment}"
export CLUBDUE_BACKUP_RETENTION_DAYS=7
if [ "${SIDEYARD_DATABASE_WINDOW_LOCK_HELD:-0}" != 1 ]; then
  prepare_sideyard_lock_root
  exec 9>"${SIDEYARD_LOCK_ROOT}/sideyard-${environment}-database-window.lock"
  flock -n 9 || { echo 'A Sideyard database or deployment operation is already running' >&2; exit 75; }
fi
load_sideyard_runtime_state "$environment"
[ "$SIDEYARD_BACKEND" = postgres ] || { echo 'Azure SQL logical backup is not implemented; refusing a stale PostgreSQL backup' >&2; exit 65; }
backup_file="$(bash "$SCRIPT_DIR/../postgres-backup.sh")"
manifest="$(python3 "$SCRIPT_DIR/backup-store.py" upload "$backup_file")"
mkdir -p /var/lib/sideyard-backups
marker="$(mktemp "/var/lib/sideyard-backups/.${environment}-success.XXXXXX")"
trap 'rm -f "$marker"' EXIT
printf '%s\nbackend=%s\ngeneration=%s\nmanifest=%s\n' "$(date -u +%s)" "$SIDEYARD_BACKEND" "$SIDEYARD_GENERATION" "$manifest" > "$marker"
chmod 600 "$marker"
mv "$marker" "/var/lib/sideyard-backups/${environment}.last-success"
trap - EXIT
echo "Sideyard ${environment} encrypted Azure backup verified"
