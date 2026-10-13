#!/usr/bin/env bash
# Authenticate Sideyard using a private copy of Pantry's Drive client settings.
set -euo pipefail

if [[ "$(hostname -s)" != g5 ]]; then
  echo 'Run this helper on G5.' >&2
  exit 1
fi
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
if [[ -n "${SSH_CONNECTION:-}" && "${1:-}" != --check ]]; then
  cat <<'EOF'
Using a browser on another computer? Open a separate LOCAL terminal there:
  ssh -N -o ExitOnForwardFailure=yes -L 53682:127.0.0.1:53682 g5
Use your usual G5 SSH destination if it differs from g5. Keep the tunnel open,
choose Y for browser authentication, and open the printed link in that browser.
EOF
fi
exec sudo /usr/bin/python3 "$SCRIPT_DIR/scripts/connect-backup-drive.py" "$@"
