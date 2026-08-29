#!/usr/bin/env bash
# Build, push, and deploy ClubDue to the pantry DO droplet.
#
# Usage:
#   ./deploy.sh staging     # default — safe to run often
#   ./deploy.sh production  # rejected — use the reviewed GitHub release workflow
#
# Requires in .env or environment:
#   GITHUB_TOKEN   — classic PAT with read:packages + write:packages
#   DROPLET_HOST   — pantry droplet IP (Pantry: terraform output -raw droplet_ip)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TARGET="${1:-staging}"

# Production promotion is intentionally unavailable from a developer machine.
# The GitHub workflow requires an immutable image digest and the production
# environment gate, which keeps the reviewed artifact and approval auditable.
case "$TARGET" in
  production|prod)
    echo "ERROR: local production deployment is disabled. After staging review and explicit approval, run the reviewed Deploy Production GitHub workflow with an immutable image digest." >&2
    exit 2
    ;;
esac

# shellcheck source=scripts/env.sh
source "${ROOT}/scripts/env.sh"

clubdue_env "$TARGET"

if [[ -f "${ROOT}/.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "${ROOT}/.env"
  set +a
fi

: "${GITHUB_TOKEN:?Set GITHUB_TOKEN (PAT with read:packages + write:packages)}"
: "${DROPLET_HOST:?Set DROPLET_HOST to the pantry droplet IP}"

echo "Building ${CLUBDUE_IMAGE} for linux/amd64..."
echo "$GITHUB_TOKEN" | docker login ghcr.io -u wmichelin --password-stdin
if docker buildx version >/dev/null 2>&1; then
  docker buildx build \
    --platform linux/amd64 \
    -t "${CLUBDUE_IMAGE}" \
    --push \
    "${ROOT}"
else
  docker build -t "${CLUBDUE_IMAGE}" "${ROOT}"
  docker push "${CLUBDUE_IMAGE}"
fi

echo "Deploying to root@${DROPLET_HOST} (${CLUBDUE_DOMAIN})..."
ssh -o StrictHostKeyChecking=accept-new "root@${DROPLET_HOST}" \
  "CONTAINER=${CLUBDUE_CONTAINER} IMAGE=${CLUBDUE_IMAGE} PORT=${CLUBDUE_PORT} DOMAIN=${CLUBDUE_DOMAIN} GHCR_USER=wmichelin GHCR_TOKEN=${GITHUB_TOKEN} EMAIL=${CLUBDUE_EMAIL} bash -s" \
  < "${ROOT}/scripts/droplet-deploy.sh"

echo "Running smoke tests..."
"${ROOT}/scripts/smoke-test.sh" "$TARGET"

echo "Done → ${CLUBDUE_URL}"
