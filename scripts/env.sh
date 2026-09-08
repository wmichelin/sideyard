#!/usr/bin/env bash
# Shared staging/production settings for ClubDue on the pantry DO droplet.
# Pantry :8080, Headscale 127.0.0.1:8081 — ClubDue does not touch those.

clubdue_env() {
  local target="${1:?usage: clubdue_env staging|production}"

  case "$target" in
    staging)
      export CLUBDUE_ENV=staging
      export CLUBDUE_CONTAINER=clubdue-staging
      export CLUBDUE_IMAGE=ghcr.io/wmichelin/clubdue:staging
      export CLUBDUE_PORT=8082
      export CLUBDUE_DOMAIN=clubdue-staging.michelindigitalmedia.com
      export CLUBDUE_URL="https://${CLUBDUE_DOMAIN}"
      ;;
    production|prod)
      export CLUBDUE_ENV=production
      export CLUBDUE_CONTAINER=clubdue
      export CLUBDUE_IMAGE=ghcr.io/wmichelin/clubdue:latest
      export CLUBDUE_PORT=8083
      export CLUBDUE_DOMAIN=getsideyard.com
      export CLUBDUE_URL="https://${CLUBDUE_DOMAIN}"
      ;;
    *)
      echo "Unknown environment: $target (use staging or production)" >&2
      return 1
      ;;
  esac

  export CLUBDUE_EMAIL="${CLUBDUE_EMAIL:-wmichelin@gmail.com}"
}
