#!/usr/bin/env bash

prepare_sideyard_lock_root() {
  SIDEYARD_LOCK_ROOT="${SIDEYARD_LOCK_ROOT:-/run/sideyard-locks}"
  if [ ! -e "$SIDEYARD_LOCK_ROOT" ]; then
    mkdir -m 700 "$SIDEYARD_LOCK_ROOT"
  fi
  [ -d "$SIDEYARD_LOCK_ROOT" ] && [ ! -L "$SIDEYARD_LOCK_ROOT" ] || {
    echo 'Sideyard lock root is missing or unsafe' >&2
    return 1
  }
  local lock_mode lock_owner
  lock_mode="$(stat -c '%a' "$SIDEYARD_LOCK_ROOT")"
  lock_owner="$(stat -c '%u:%g' "$SIDEYARD_LOCK_ROOT")"
  [ "$lock_mode" = 700 ] || { echo 'Sideyard lock root must have mode 700' >&2; return 1; }
  if [ -z "${SIDEYARD_STATE_TEST_OWNER:-}" ]; then
    [ "$lock_owner" = 0:0 ] || { echo 'Sideyard lock root must be owned by root' >&2; return 1; }
  fi
  export SIDEYARD_LOCK_ROOT
}

load_sideyard_runtime_state() {
  local environment="$1" state_root="${SIDEYARD_STATE_ROOT:-/var/lib/sideyard-operations}"
  local state_file="${state_root}/${environment}-backend.env" state_mode state_owner line_count
  [ -f "$state_file" ] && [ ! -L "$state_file" ] || { echo 'Sideyard backend state is missing or unsafe' >&2; return 1; }
  state_mode="$(stat -c '%a' "$state_file")"
  state_owner="$(stat -c '%u:%g' "$state_file")"
  [ "$state_mode" = 600 ] || { echo 'Sideyard backend state must have mode 600' >&2; return 1; }
  if [ -z "${SIDEYARD_STATE_TEST_OWNER:-}" ]; then
    [ "$state_owner" = 0:0 ] || { echo 'Sideyard backend state must be owned by root' >&2; return 1; }
  fi
  line_count="$(wc -l < "$state_file")"
  [ "$line_count" = 2 ] || { echo 'Sideyard backend state must contain exactly two fields' >&2; return 1; }
  SIDEYARD_BACKEND="$(sed -n 's/^DATABASE_BACKEND=//p' "$state_file")"
  SIDEYARD_GENERATION="$(sed -n 's/^DATA_GENERATION=//p' "$state_file")"
  [ "$(grep -c '^DATABASE_BACKEND=' "$state_file")" = 1 ] || { echo 'Sideyard backend state is invalid' >&2; return 1; }
  [ "$(grep -c '^DATA_GENERATION=' "$state_file")" = 1 ] || { echo 'Sideyard backend state is invalid' >&2; return 1; }
  case "$SIDEYARD_BACKEND" in postgres|azure_sql) ;; *) echo 'Sideyard backend state is invalid' >&2; return 1 ;; esac
  [[ "$SIDEYARD_GENERATION" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$ ]] || { echo 'Sideyard data generation is invalid' >&2; return 1; }
  export SIDEYARD_BACKEND SIDEYARD_GENERATION
}

verify_sideyard_current_container() {
  local environment="$1" container="$2" running backend generation
  running="$(docker inspect -f '{{.State.Running}}' "$container" 2>/dev/null || true)"
  backend="$(docker inspect -f '{{ index .Config.Labels "sideyard.backend" }}' "$container" 2>/dev/null || true)"
  generation="$(docker inspect -f '{{ index .Config.Labels "sideyard.generation" }}' "$container" 2>/dev/null || true)"
  [ "$running" = true ] && [ "$backend" = "$SIDEYARD_BACKEND" ] && [ "$generation" = "$SIDEYARD_GENERATION" ] || {
    echo "Sideyard ${environment} current container does not match authoritative data" >&2
    return 1
  }
}
