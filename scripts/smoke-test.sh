#!/usr/bin/env bash
# Smoke-test a deployed ClubDue environment (staging or production).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/env.sh
source "${SCRIPT_DIR}/env.sh"

TARGET="${1:-staging}"
clubdue_env "$TARGET"

echo "Smoke-testing ${CLUBDUE_ENV} at ${CLUBDUE_URL} ..."

curl -fsS "${CLUBDUE_URL}/healthz" | grep -qx ok
echo "  ✓ /healthz"

HTML="$(curl -fsS "${CLUBDUE_URL}/")"
echo "$HTML" | grep -q 'ClubDue'
echo "  ✓ dashboard title"

echo "$HTML" | grep -q 'Alex Rivera'
echo "  ✓ sample member row"

echo "$HTML" | grep -q 'Collected'
echo "  ✓ summary stats"

echo "All checks passed for ${CLUBDUE_URL}"
