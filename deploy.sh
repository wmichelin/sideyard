#!/usr/bin/env bash
# Build, push, and deploy ClubDue to the pantry DO droplet.
#
# Usage:
#   ./deploy.sh staging     # default — safe to run often
#   ./deploy.sh production  # prod — prefer GitHub Actions after staging sign-off
#
# Requires in .env or environment:
#   GITHUB_TOKEN   — classic PAT with read:packages + write:packages
#   DROPLET_HOST   — pantry droplet IP (Pantry: terraform output -raw droplet_ip)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/env.sh
source "${ROOT}/scripts/env.sh"

TARGET="${1:-staging}"
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
docker buildx build \
  --platform linux/amd64 \
  -t "${CLUBDUE_IMAGE}" \
  --push \
  "${ROOT}"

echo "Deploying to root@${DROPLET_HOST} (${CLUBDUE_DOMAIN})..."
ssh -o StrictHostKeyChecking=accept-new "root@${DROPLET_HOST}" \
  "CONTAINER=${CLUBDUE_CONTAINER} IMAGE=${CLUBDUE_IMAGE} PORT=${CLUBDUE_PORT} DOMAIN=${CLUBDUE_DOMAIN} GHCR_USER=wmichelin GHCR_TOKEN=${GITHUB_TOKEN} EMAIL=${CLUBDUE_EMAIL} bash -s" \
  < "${ROOT}/scripts/droplet-deploy.sh"

echo "Running smoke tests..."
"${ROOT}/scripts/smoke-test.sh" "$TARGET"

echo "Done → ${CLUBDUE_URL}"
