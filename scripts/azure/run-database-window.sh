#!/usr/bin/env bash
set -uo pipefail

environment="${1:?usage: run-database-window.sh staging}"
[ "$environment" = staging ] || { echo 'Unified database window is staging-only until production review' >&2; exit 64; }
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/runtime-state.sh"
prepare_sideyard_lock_root || exit $?
exec 9>"${SIDEYARD_LOCK_ROOT}/sideyard-${environment}-database-window.lock"
flock -w 1200 9 || { echo 'Another Sideyard database or deployment operation is active' >&2; exit 75; }
load_sideyard_runtime_state "$environment" || exit $?
container="clubdue-staging-current"
verify_sideyard_current_container "$environment" "$container" || exit $?

work_status=0
docker exec "$container" /clubdue scheduled-work || work_status=$?
backup_status=0
backup_runner="${SIDEYARD_BACKUP_RUNNER:-$SCRIPT_DIR/run-backup.sh}"
SIDEYARD_DATABASE_WINDOW_LOCK_HELD=1 bash "$backup_runner" "$environment" || backup_status=$?

if [ "$work_status" -ne 0 ] || [ "$backup_status" -ne 0 ]; then
  echo "Sideyard database window failed scheduled_work=${work_status} backup=${backup_status}" >&2
  exit 1
fi
operations_root="${SIDEYARD_OPERATIONS_ROOT:-/var/lib/sideyard-operations}"
mkdir -p "$operations_root"
marker="$(mktemp "${operations_root}/.${environment}-database-window.XXXXXX")"
printf 'completed_at=%s\nbackend=%s\ngeneration=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$SIDEYARD_BACKEND" "$SIDEYARD_GENERATION" > "$marker"
chmod 600 "$marker"
mv "$marker" "${operations_root}/${environment}-database-window.last-success"
echo "Sideyard ${environment} database window complete"
