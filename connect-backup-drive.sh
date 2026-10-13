#!/usr/bin/env bash
# Authenticate Sideyard using a private copy of Pantry's Drive client settings.
set -euo pipefail

if [[ "$(hostname -s)" != g5 ]]; then
  echo 'Run this helper on G5.' >&2
  exit 1
fi
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
exec sudo /usr/bin/python3 "$SCRIPT_DIR/scripts/connect-backup-drive.py" "$@"
