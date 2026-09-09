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
umask 077
export CLUBDUE_BACKUP_DIR="/var/backups/sideyard-azure/${environment}"
export CLUBDUE_BACKUP_RETENTION_DAYS=7
exec 9>"/run/lock/sideyard-${environment}-azure-backup.lock"
flock -n 9 || { echo 'A Sideyard backup is already running' >&2; exit 75; }
backup_file="$(bash "$SCRIPT_DIR/../postgres-backup.sh")"
manifest="$(python3 "$SCRIPT_DIR/backup-store.py" upload "$backup_file")"
mkdir -p /var/lib/sideyard-backups
marker="$(mktemp "/var/lib/sideyard-backups/.${environment}-success.XXXXXX")"
trap 'rm -f "$marker"' EXIT
printf '%s\n%s\n' "$(date -u +%s)" "$manifest" > "$marker"
mv "$marker" "/var/lib/sideyard-backups/${environment}.last-success"
trap - EXIT
echo "Sideyard ${environment} encrypted Azure backup verified"
