#!/usr/bin/env bash
# Runs ON the droplet (via SSH). Installs/updates one ClubDue container + nginx vhost.
set -euo pipefail

: "${CONTAINER:?CONTAINER required}"
: "${PORT:?PORT required}"

# ClubDue shares this droplet with Pantry and Headscale. Validate the target
# before installing packages, logging into a registry, or mutating Docker so a
# mistaken deployment cannot affect either reserved local port.
case "$PORT" in
  8080|8081)
    echo "ERROR: ClubDue may not target reserved port ${PORT}" >&2
    exit 64
    ;;
esac

case "$CONTAINER" in
  clubdue-staging)
    if [ "$PORT" != "8082" ]; then
      echo "ERROR: staging must use isolated port 8082" >&2
      exit 64
    fi
    ;;
  clubdue)
    if [ "$PORT" != "8083" ]; then
      echo "ERROR: production must use isolated port 8083" >&2
      exit 64
    fi
    ;;
  *)
    echo "ERROR: unsupported ClubDue container ${CONTAINER}" >&2
    exit 64
    ;;
esac

: "${IMAGE:?IMAGE required}"
: "${DOMAIN:?DOMAIN required}"
: "${GHCR_USER:?GHCR_USER required}"
: "${GHCR_TOKEN:?GHCR_TOKEN required}"

EMAIL="${EMAIL:-wmichelin@gmail.com}"

echo "==> ClubDue deploy: ${CONTAINER} (${DOMAIN}) ← ${IMAGE} on 127.0.0.1:${PORT}"

if ! command -v docker >/dev/null 2>&1; then
  apt-get update -y && apt-get install -y docker.io
  systemctl enable docker && systemctl start docker
fi

if ! command -v nginx >/dev/null 2>&1; then
  apt-get update -y && apt-get install -y nginx certbot python3-certbot-nginx
fi

echo "$GHCR_TOKEN" | docker login ghcr.io -u "$GHCR_USER" --password-stdin
docker pull "$IMAGE"

if [ "$CONTAINER" = "clubdue-staging" ]; then
  STAGING_NETWORK="clubdue-staging-net"
  STAGING_DB="clubdue-staging-db"
  STAGING_VOLUME="clubdue-staging-pgdata"
  STAGING_ENV_DIR="/opt/clubdue"
  STAGING_ENV_FILE="${STAGING_ENV_DIR}/staging-db.env"

  docker network create "$STAGING_NETWORK" >/dev/null 2>&1 || true
  docker volume create "$STAGING_VOLUME" >/dev/null
  mkdir -p "$STAGING_ENV_DIR"
  if [ ! -f "$STAGING_ENV_FILE" ]; then
    umask 077
    db_password="$(openssl rand -hex 24)"
    session_secret="$(openssl rand -hex 32)"
    {
      printf 'POSTGRES_PASSWORD=%s\n' "$db_password"
      printf 'SESSION_SECRET=%s\n' "$session_secret"
    } > "$STAGING_ENV_FILE"
  fi
  # shellcheck disable=SC1090
  set -a; . "$STAGING_ENV_FILE"; set +a

  docker rm -f "$STAGING_DB" >/dev/null 2>&1 || true
  docker run -d --name "$STAGING_DB" --restart always --network "$STAGING_NETWORK" \
    -e POSTGRES_USER=clubdue -e POSTGRES_DB=clubdue -e POSTGRES_PASSWORD \
    -v "${STAGING_VOLUME}:/var/lib/postgresql/data" postgres:16-alpine >/dev/null
  for attempt in $(seq 1 30); do
    if docker exec "$STAGING_DB" pg_isready -U clubdue -d clubdue >/dev/null 2>&1; then break; fi
    sleep 1
  done
  docker exec "$STAGING_DB" pg_isready -U clubdue -d clubdue >/dev/null
else
  # Production provisioning is deliberately performed only by an explicitly
  # approved workflow. This branch never creates credentials: the operator
  # must first create this root-only, production-specific file out of band.
  PRODUCTION_NETWORK="clubdue-production-net"
  PRODUCTION_DB="clubdue-production-db"
  PRODUCTION_VOLUME="clubdue-production-pgdata"
  PRODUCTION_ENV_FILE="/opt/clubdue/production.env"

  if [ ! -r "$PRODUCTION_ENV_FILE" ]; then
    echo "ERROR: production environment file is missing" >&2
    exit 1
  fi
  production_env_mode="$(stat -c '%a' "$PRODUCTION_ENV_FILE")"
  if [ $((8#$production_env_mode & 077)) -ne 0 ]; then
    echo "ERROR: production environment file must not be group/world readable" >&2
    exit 1
  fi
  # shellcheck disable=SC1090
  set -a; . "$PRODUCTION_ENV_FILE"; set +a
  : "${POSTGRES_PASSWORD:?POSTGRES_PASSWORD required in production environment file}"
  : "${SESSION_SECRET:?SESSION_SECRET required in production environment file}"

  docker network create "$PRODUCTION_NETWORK" >/dev/null 2>&1 || true
  docker volume create "$PRODUCTION_VOLUME" >/dev/null
  docker rm -f "$PRODUCTION_DB" >/dev/null 2>&1 || true
  docker run -d --name "$PRODUCTION_DB" --restart always --network "$PRODUCTION_NETWORK" \
    -e POSTGRES_USER=clubdue -e POSTGRES_DB=clubdue -e POSTGRES_PASSWORD \
    -v "${PRODUCTION_VOLUME}:/var/lib/postgresql/data" postgres:16-alpine >/dev/null
  for attempt in $(seq 1 30); do
    if docker exec "$PRODUCTION_DB" pg_isready -U clubdue -d clubdue >/dev/null 2>&1; then break; fi
    sleep 1
  done
  docker exec "$PRODUCTION_DB" pg_isready -U clubdue -d clubdue >/dev/null
fi

docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
app_args=(docker run -d --name "$CONTAINER" --restart always -p "127.0.0.1:${PORT}:8080")
if [ "$CONTAINER" = "clubdue-staging" ]; then
  app_args+=(--network clubdue-staging-net -e APP_ENV=staging -e "PUBLIC_URL=https://${DOMAIN}" -e "DATABASE_URL=postgres://clubdue:${POSTGRES_PASSWORD}@clubdue-staging-db:5432/clubdue?sslmode=disable" -e "SESSION_SECRET=${SESSION_SECRET}" -e ENABLE_DEV_ROUTES=1)
else
  app_args+=(--network clubdue-production-net -e APP_ENV=production -e "PUBLIC_URL=https://${DOMAIN}" -e "DATABASE_URL=postgres://clubdue:${POSTGRES_PASSWORD}@clubdue-production-db:5432/clubdue?sslmode=disable" -e "SESSION_SECRET=${SESSION_SECRET}")
fi
"${app_args[@]}" \
  "$IMAGE" >/dev/null

if [ "$CONTAINER" = "clubdue-staging" ]; then
  # Staging already has a provisioned TLS vhost. Keep this remote step focused
  # on replacing the isolated container; the workflow's next step smoke-tests
  # the public staging URL.
  for attempt in $(seq 1 15); do
    if curl -fsS "http://127.0.0.1:${PORT}/readyz" >/dev/null; then
      echo "==> Staging container ready at http://127.0.0.1:${PORT}/readyz"
      exit 0
    fi
    sleep 1
  done
  echo "ERROR: ${CONTAINER} did not become healthy within 15 seconds" >&2
  exit 1
fi

# Status output is diagnostic only; Docker may report a transient non-zero while
# the newly created container is still being registered. The health probe below
# is the deploy gate.
docker ps --filter "name=^${CONTAINER}$" --format '{{.Names}} {{.Status}} {{.Image}}' || true

if [[ ! -f "/etc/letsencrypt/live/${DOMAIN}/fullchain.pem" ]]; then
  cat > "/etc/nginx/sites-available/${DOMAIN}" <<NGINX
server {
    listen 80;
    server_name ${DOMAIN};

    location / {
        proxy_pass http://127.0.0.1:${PORT};
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
    }
}
NGINX
  ln -sf "/etc/nginx/sites-available/${DOMAIN}" "/etc/nginx/sites-enabled/${DOMAIN}"
  rm -f /etc/nginx/sites-enabled/default
  nginx -t && systemctl reload nginx
  if ! certbot --nginx -d "$DOMAIN" --non-interactive --agree-tos -m "$EMAIL" --redirect; then
    echo "WARNING: certbot failed — add DNS A record for ${DOMAIN} → droplet IP, then re-run." >&2
  fi
else
  if ! grep -q "127.0.0.1:${PORT}" "/etc/nginx/sites-available/${DOMAIN}" 2>/dev/null; then
    echo "WARNING: nginx site exists but may not proxy to 127.0.0.1:${PORT} — check manually." >&2
  fi
  # Existing TLS vhosts already proxy this stable local port. Deploying a new
  # container image does not change nginx configuration, so no reload is needed.
fi

for attempt in $(seq 1 15); do
  if curl -fsS "http://127.0.0.1:${PORT}/readyz" >/dev/null; then
    echo "==> Ready at http://127.0.0.1:${PORT}/readyz"
    exit 0
  fi
  sleep 1
done

echo "ERROR: ${CONTAINER} did not become healthy within 15 seconds" >&2
exit 1
