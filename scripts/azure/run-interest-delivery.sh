#!/usr/bin/env bash
set -euo pipefail

environment="${1:?usage: run-interest-delivery.sh staging}"
[ "$environment" = staging ] || { echo 'Host interest delivery is staging-only until production review' >&2; exit 64; }
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/runtime-state.sh"
prepare_sideyard_lock_root
exec 9>"${SIDEYARD_LOCK_ROOT}/sideyard-${environment}-database-window.lock"
flock -w 120 9 || { echo 'Another Sideyard database or deployment operation is active' >&2; exit 75; }
load_sideyard_runtime_state "$environment"
[ "$SIDEYARD_BACKEND" = azure_sql ] || { echo 'Host interest delivery requires Azure SQL authority' >&2; exit 65; }

wake_root="${SIDEYARD_WAKE_ROOT:-/run/sideyard}"
container="clubdue-staging-current"
wake="${wake_root}/${environment}/interest.wake"
claimed="${wake}.claimed"
marker_is_safe() {
  [ -f "$1" ] && [ ! -L "$1" ]
}
# The shared database lock serializes claims. Recover a claim abandoned by
# SIGKILL, OOM or host loss before deciding there is no work.
if [ -e "$claimed" ] || [ -L "$claimed" ]; then
  marker_is_safe "$claimed" || { echo 'Sideyard claimed interest wake marker is unsafe' >&2; exit 65; }
  if [ -e "$wake" ] || [ -L "$wake" ]; then
    marker_is_safe "$wake" || { echo 'Sideyard interest wake marker is unsafe' >&2; exit 65; }
    rm -f -- "$claimed"
  else
    mv -- "$claimed" "$wake"
  fi
fi
[ -e "$wake" ] || exit 0
marker_is_safe "$wake" || { echo 'Sideyard interest wake marker is unsafe' >&2; exit 65; }
verify_sideyard_current_container "$environment" "$container"
restore_wake() {
  if [ -e "$claimed" ]; then
    if [ -e "$wake" ]; then
      rm -f -- "$claimed"
    else
      mv -- "$claimed" "$wake"
    fi
  elif [ ! -e "$wake" ]; then
    : > "$wake"
    chmod 600 "$wake"
  fi
}
trap restore_wake EXIT
batch=0
while [ "$batch" -lt 10 ]; do
  if [ -e "$wake" ]; then
    [ -f "$wake" ] && [ ! -L "$wake" ] || { echo 'Sideyard interest wake marker is unsafe' >&2; exit 65; }
    mv -- "$wake" "$claimed"
    if [ -n "${SIDEYARD_STATE_TEST_OWNER:-}" ] && [ "${SIDEYARD_TEST_HARD_KILL_AFTER_CLAIM:-0}" = 1 ]; then
      kill -KILL "$$"
    fi
  elif [ ! -e "$claimed" ]; then
    trap - EXIT
    exit 0
  fi
  set +e
  docker exec "$container" /clubdue deliver-interest
  command_status=$?
  set -e
  case "$command_status" in
    0)
      rm -f -- "$claimed"
      if [ -e "$wake" ]; then
        batch=$((batch + 1))
      else
        trap - EXIT
        exit 0
      fi
      ;;
    75)
      restore_wake
      batch=$((batch + 1))
      ;;
    *) exit "$command_status" ;;
  esac
done
echo 'Sideyard interest delivery batch limit reached; wake retained for retry' >&2
exit 75
