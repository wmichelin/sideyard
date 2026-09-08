#!/usr/bin/env bash
# Runs on the ClubDue droplet through the reviewed deployment workflows. It
# manages only ClubDue containers and the single ClubDue nginx vhost.
set -euo pipefail

: "${CONTAINER:?CONTAINER required}"
: "${PORT:?PORT required}"

fail() { echo "ERROR: $*" >&2; exit 1; }

validate_target() {
  # Pantry and Headscale are separate services. They are never ClubDue
  # upstreams, candidates, or rollback destinations.
  case "$PORT" in 8080|8081) fail "ClubDue may not target reserved port ${PORT}" ;; esac
  case "$CONTAINER:$PORT" in
    clubdue-staging:8082) ALT_PORT=18082 ;;
    # Pantry staging owns 127.0.0.1:18083. Keep ClubDue's rollback slot
    # isolated so a production candidate never competes for that listener.
    clubdue:8083) ALT_PORT=28083 ;;
    *) fail "unsupported ClubDue deployment target ${CONTAINER} on ${PORT}" ;;
  esac
}

rollout_plan() {
  # Narrow Docker-only test seam. It has no access to a droplet or its state.
  case "${CLUBDUE_DEPLOY_TEST_PLAN:?CLUBDUE_DEPLOY_TEST_PLAN required}" in
    candidate) printf 'candidate=%s active=%s failure=preserve-active public-demo=%s\n' "$ALT_PORT" "$PORT" "$DEPLOY_PUBLIC_DEMO" ;;
    promote) printf 'proxy=%s active=%s previous=%s\n' "$ALT_PORT" "$ALT_PORT" "$PORT" ;;
    next-promote) printf 'release-previous=%s candidate=%s active=%s previous=%s\n' "$PORT" "$PORT" "$PORT" "$ALT_PORT" ;;
    rollback) printf 'proxy=%s active=%s previous=%s\n' "$PORT" "$PORT" "$ALT_PORT" ;;
    *) fail "unsupported deployment test plan ${CLUBDUE_DEPLOY_TEST_PLAN}" ;;
  esac
}

validate_target

if [ "$CONTAINER" = clubdue-staging ]; then
  DEPLOY_PUBLIC_DEMO=1
else
  DEPLOY_PUBLIC_DEMO="${CLUBDUE_DEPLOY_PUBLIC_DEMO:-0}"
  case "$DEPLOY_PUBLIC_DEMO" in 0|1) ;; *) fail "CLUBDUE_DEPLOY_PUBLIC_DEMO must be 0 or 1" ;; esac
fi
readonly DEPLOY_PUBLIC_DEMO

if [ -n "${CLUBDUE_DEPLOY_TEST_PLAN:-}" ]; then rollout_plan; exit 0; fi

DEPLOY_MODE="${CLUBDUE_DEPLOY_MODE:-promote}"
case "$DEPLOY_MODE" in promote|rollback) ;; *) fail "CLUBDUE_DEPLOY_MODE must be promote or rollback" ;; esac
if [ "${CLUBDUE_DEPLOY_VALIDATE_ONLY:-}" = "1" ]; then
  echo "ClubDue deployment target validated: ${CONTAINER} on ${PORT}; alternate ${ALT_PORT}"
  exit 0
fi

: "${DOMAIN:?DOMAIN required}"
if [ "$DEPLOY_MODE" = promote ]; then
  : "${IMAGE:?IMAGE required}"
  : "${GHCR_USER:?GHCR_USER required}"
  : "${GHCR_TOKEN:?GHCR_TOKEN required}"
fi

SITE_PATH="/etc/nginx/sites-available/${DOMAIN}"
ENABLED_PATH="/etc/nginx/sites-enabled/${DOMAIN}"
CURRENT_CONTAINER="${CONTAINER}-current"
PREVIOUS_CONTAINER="${CONTAINER}-previous"
CANDIDATE_CONTAINER="${CONTAINER}-candidate"

container_exists() { docker container inspect "$1" >/dev/null 2>&1; }

container_port() {
  local mapping
  mapping="$(docker port "$1" 8080/tcp 2>/dev/null || true)"
  case "$mapping" in
    127.0.0.1:8082|127.0.0.1:18082|127.0.0.1:8083|127.0.0.1:28083) printf '%s\n' "${mapping##*:}" ;;
    *) fail "$1 does not expose a supported ClubDue loopback port" ;;
  esac
}

wait_for_database() {
  local db="$1"
  for _ in $(seq 1 30); do
    docker exec "$db" pg_isready -U clubdue -d clubdue >/dev/null 2>&1 && return 0
    sleep 1
  done
  fail "${db} did not become ready"
}

ensure_database() {
  local network="$1" db="$2" volume="$3"
  docker network create "$network" >/dev/null 2>&1 || true
  docker volume create "$volume" >/dev/null
  # Ordinary app deploys preserve the environment DB and volume. A stopped DB
  # is restarted; an unhealthy DB fails closed for operator investigation.
  if container_exists "$db"; then
    if [ "$(docker inspect -f '{{.State.Running}}' "$db")" != true ]; then docker start "$db" >/dev/null; fi
    wait_for_database "$db"
    return
  fi
  docker run -d --name "$db" --restart always --network "$network" \
    -e POSTGRES_USER=clubdue -e POSTGRES_DB=clubdue -e POSTGRES_PASSWORD \
    -v "${volume}:/var/lib/postgresql/data" postgres:16-alpine >/dev/null
  wait_for_database "$db"
}

assert_safe_vhost() {
  [ -f "$SITE_PATH" ] || fail "ClubDue nginx vhost is missing: ${SITE_PATH}"
  [ -L "$ENABLED_PATH" ] || fail "ClubDue nginx vhost is not enabled: ${ENABLED_PATH}"
  [ "$(readlink -f "$ENABLED_PATH")" = "$(readlink -f "$SITE_PATH")" ] || fail "enabled ClubDue nginx vhost does not resolve to ${SITE_PATH}"
  local server_count upstream_count
  server_count="$(awk -v domain="$DOMAIN" '
    $1 == "server_name" {
      for (field = 2; field <= NF; field++) {
        value = $field
        sub(/;$/, "", value)
        if (value == domain) count++
      }
    }
    END { print count + 0 }
  ' "$SITE_PATH")"
  upstream_count="$(grep -Ec "proxy_pass[[:space:]]+http://127\\.0\\.0\\.1:(${PORT}|${ALT_PORT});" "$SITE_PATH" || true)"
  # Certbot commonly keeps both an HTTP redirect block and an HTTPS proxy
  # block in one vhost file, so the domain can legitimately appear twice.
  # The single managed proxy upstream is the traffic-switch safety boundary.
  [ "$server_count" -ge 1 ] || fail "ClubDue nginx vhost cannot be identified safely"
  [ "$upstream_count" = 1 ] || fail "ClubDue nginx vhost has no unique managed ClubDue upstream"
  if grep -Eq "proxy_pass[[:space:]]+http://127\\.0\\.0\\.1:(8080|8081);" "$SITE_PATH"; then
    fail "ClubDue nginx vhost references a reserved service port"
  fi
}

switch_proxy() {
  local target_port="$1" site_dir candidate backup
  assert_safe_vhost
  site_dir="$(dirname "$SITE_PATH")"
  candidate="$(mktemp "${site_dir}/.clubdue-vhost.XXXXXX")"
  backup="$(mktemp "${site_dir}/.clubdue-vhost-backup.XXXXXX")"
  cp --preserve=mode,ownership "$SITE_PATH" "$candidate"
  cp --preserve=mode,ownership "$SITE_PATH" "$backup"
  sed -E -i "s#proxy_pass[[:space:]]+http://127\\.0\\.0\\.1:(${PORT}|${ALT_PORT});#proxy_pass http://127.0.0.1:${target_port};#" "$candidate"
  mv "$candidate" "$SITE_PATH"
  if ! nginx -t; then
    cp --preserve=mode,ownership "$backup" "$SITE_PATH"; rm -f "$backup"
    fail "nginx rejected the ClubDue vhost update; active proxy was preserved"
  fi
  if ! systemctl reload nginx; then
    cp --preserve=mode,ownership "$backup" "$SITE_PATH"
    nginx -t || true; systemctl reload nginx || true; rm -f "$backup"
    fail "nginx reload failed; restored the previous ClubDue upstream"
  fi
  rm -f "$backup"
}

wait_for_ready() {
  local target_port="$1"
  for _ in $(seq 1 30); do
    curl -fsS "http://127.0.0.1:${target_port}/readyz" 2>/dev/null | grep -qx ok && return 0
    sleep 1
  done
  return 1
}

verify_candidate_contract() {
  local target_port="$1" expected_demo="$2" demo_headers demo_body demo_status marker
  marker="$(curl -fsS --max-time 10 "http://127.0.0.1:${target_port}/rollback-safetyz" 2>/dev/null || true)"
  [ "$marker" = "demo-sandbox-v1" ] || fail "candidate is missing the demo-sandbox safety marker; active release remains unchanged"

  CANDIDATE_PROBE_DIR="$(mktemp -d)"
  demo_headers="$CANDIDATE_PROBE_DIR/demo.headers"
  demo_body="$CANDIDATE_PROBE_DIR/demo.html"
  demo_status="$(curl -sS --max-time 20 -D "$demo_headers" -o "$demo_body" -w '%{http_code}' "http://127.0.0.1:${target_port}/demo")"
  if [ "$expected_demo" = 0 ]; then
    [ "$demo_status" = 404 ] || fail "candidate unexpectedly exposes the public demo; active release remains unchanged"
    rm -rf "$CANDIDATE_PROBE_DIR"
    CANDIDATE_PROBE_DIR=""
    return
  fi

  [ "$demo_status" = 200 ] || fail "candidate public demo entry is unavailable; active release remains unchanged"
  if ! grep -q 'action="/demo/session"' "$demo_body" ||
    ! grep -Eiq '^Cache-Control:.*no-store' "$demo_headers" ||
    ! grep -Eiq '^X-Robots-Tag:.*noindex' "$demo_headers" ||
    grep -Eiq '^Set-Cookie:[[:space:]]*clubdue_session=' "$demo_headers"; then
    fail "candidate public demo entry failed its non-mutating safety contract; active release remains unchanged"
  fi

  local csrf_token csrf_cookie session_headers session_status session_cookie dashboard
  csrf_token="$(sed -n 's/.*name="csrf_token" value="\([^"]*\)".*/\1/p' "$demo_body" | head -1)"
  csrf_cookie="$(sed -n 's/^Set-Cookie: clubdue_csrf=\([^;]*\).*/\1/pI' "$demo_headers" | tail -1)"
  if [ -z "$csrf_token" ] || [ "$csrf_token" != "$csrf_cookie" ]; then
    fail "candidate public demo CSRF contract failed; active release remains unchanged"
  fi
  session_headers="$CANDIDATE_PROBE_DIR/session.headers"
  session_status="$(curl -sS --max-time 20 -D "$session_headers" -o /dev/null -w '%{http_code}' -X POST \
    -H "Cookie: clubdue_csrf=${csrf_cookie}" \
    --data-urlencode "csrf_token=${csrf_token}" \
    "http://127.0.0.1:${target_port}/demo/session")"
  if [ "$session_status" != 303 ] || ! grep -Eiq '^Location:[[:space:]]*/dashboard' "$session_headers"; then
    fail "candidate could not create an isolated demo session; active release remains unchanged"
  fi
  if ! grep -Eiq '^Set-Cookie:[[:space:]]*clubdue_session=.*Secure' "$session_headers" ||
    ! grep -Eiq '^Set-Cookie:[[:space:]]*clubdue_session=.*HttpOnly' "$session_headers" ||
    ! grep -Eiq '^Set-Cookie:[[:space:]]*clubdue_session=.*SameSite=Lax' "$session_headers"; then
    fail "candidate demo session cookie contract failed; active release remains unchanged"
  fi
  session_cookie="$(sed -n 's/^Set-Cookie: clubdue_session=\([^;]*\).*/\1/pI' "$session_headers" | tail -1)"
  [ -n "$session_cookie" ] || fail "candidate demo session cookie is missing; active release remains unchanged"
  dashboard="$(curl -fsS --max-time 20 \
    -H "Cookie: clubdue_csrf=${csrf_cookie}; clubdue_session=${session_cookie}" \
    "http://127.0.0.1:${target_port}/dashboard")"
  # grep -q can close its pipe early and give printf SIGPIPE under pipefail,
  # rejecting a valid page. Match the already buffered response directly.
  [[ "$dashboard" == *'Fictional public demo'* ]] || fail "candidate demo fixture did not resolve; active release remains unchanged"
  [[ "$dashboard" == *'Evergreen Lawn &amp; Landscape'* ]] || fail "candidate demo fixture is incomplete; active release remains unchanged"
  rm -rf "$CANDIDATE_PROBE_DIR"
  CANDIDATE_PROBE_DIR=""
}

if ! command -v docker >/dev/null 2>&1; then
  apt-get update -y && apt-get install -y docker.io
  systemctl enable docker && systemctl start docker
fi
command -v nginx >/dev/null 2>&1 || fail "nginx must be installed and the ClubDue vhost provisioned before deployment"

if [ "$DEPLOY_MODE" = rollback ]; then
  container_exists "$CURRENT_CONTAINER" || fail "no current ClubDue release is available to roll back"
  container_exists "$PREVIOUS_CONTAINER" || fail "no retained previous ClubDue release is available"
  current_port="$(container_port "$CURRENT_CONTAINER")"
  previous_port="$(container_port "$PREVIOUS_CONTAINER")"
  [ "$current_port" != "$previous_port" ] || fail "current and previous ClubDue releases share a port"
  wait_for_ready "$previous_port" || fail "retained previous ClubDue release is not ready"
  rollback_marker="$(curl -fsS "http://127.0.0.1:${previous_port}/rollback-safetyz" 2>/dev/null || true)"
  [ "$rollback_marker" = "demo-sandbox-v1" ] || fail "retained release predates demo-sandbox safety; refusing unsafe rollback"
  switch_proxy "$previous_port"
  docker rename "$CURRENT_CONTAINER" "$CANDIDATE_CONTAINER"
  docker rename "$PREVIOUS_CONTAINER" "$CURRENT_CONTAINER"
  docker rename "$CANDIDATE_CONTAINER" "$PREVIOUS_CONTAINER"
  echo "==> Rolled back ${CONTAINER}: nginx now targets ${CURRENT_CONTAINER} on 127.0.0.1:${previous_port}"
  exit 0
fi

echo "==> ClubDue candidate deploy: ${CONTAINER} (${DOMAIN}) ← ${IMAGE}"
echo "$GHCR_TOKEN" | docker login ghcr.io -u "$GHCR_USER" --password-stdin
docker pull "$IMAGE"

if [ "$CONTAINER" = clubdue-staging ]; then
  ENV_FILE=/opt/clubdue/staging-db.env
  NETWORK=clubdue-staging-net; DB=clubdue-staging-db; VOLUME=clubdue-staging-pgdata
  mkdir -p /opt/clubdue
  if [ ! -f "$ENV_FILE" ]; then
    umask 077
    { printf 'POSTGRES_PASSWORD=%s\n' "$(openssl rand -hex 24)"; printf 'SESSION_SECRET=%s\n' "$(openssl rand -hex 32)"; } > "$ENV_FILE"
  fi
  set -a; . "$ENV_FILE"; set +a
  ensure_database "$NETWORK" "$DB" "$VOLUME"
  APP_ENV=( -e APP_ENV=staging -e ENABLE_PUBLIC_DEMO=1 )
else
  ENV_FILE=/opt/clubdue/production.env
  NETWORK=clubdue-production-net; DB=clubdue-production-db; VOLUME=clubdue-production-pgdata
  [ -r "$ENV_FILE" ] || fail "production environment file is missing"
  env_mode="$(stat -c '%a' "$ENV_FILE")"
  [ $((8#$env_mode & 077)) -eq 0 ] || fail "production environment file must not be group/world readable"
  set -a; . "$ENV_FILE"; set +a
  : "${POSTGRES_PASSWORD:?POSTGRES_PASSWORD required in production environment file}"
  : "${SESSION_SECRET:?SESSION_SECRET required in production environment file}"
  ensure_database "$NETWORK" "$DB" "$VOLUME"
  APP_ENV=( -e APP_ENV=production -e "ENABLE_PUBLIC_DEMO=${DEPLOY_PUBLIC_DEMO}" )
fi

if container_exists "$CURRENT_CONTAINER"; then
  active_port="$(container_port "$CURRENT_CONTAINER")"
elif container_exists "$CONTAINER"; then
  # One-time migration from legacy stable container naming.
  active_port="$(container_port "$CONTAINER")"
else
  fail "no existing stable ${CONTAINER} release; provision and verify the ClubDue vhost before using reversible deploy"
fi
[ "$active_port" = "$PORT" ] || [ "$active_port" = "$ALT_PORT" ] || fail "unexpected active ClubDue port"
candidate_port="$ALT_PORT"; [ "$active_port" = "$ALT_PORT" ] && candidate_port="$PORT"

assert_safe_vhost
active_marker="$(curl -fsS "http://127.0.0.1:${active_port}/rollback-safetyz" 2>/dev/null || true)"
[ "$active_marker" = "demo-sandbox-v1" ] || fail "active release predates demo-sandbox safety; refusing to replace the known rollback target"
if container_exists "$CANDIDATE_CONTAINER"; then
  if [ "$(docker inspect -f '{{.State.Running}}' "$CANDIDATE_CONTAINER")" = true ]; then
    fail "running ClubDue candidate exists; inspect it before retrying"
  fi
  # A failed Docker start can leave a stopped candidate behind. It never
  # served traffic, so removing it is safe and makes the next rollout retryable.
  docker rm "$CANDIDATE_CONTAINER" >/dev/null
fi
# The retained previous release occupies the alternate port needed by the next
# candidate. Releasing that stale rollback slot leaves the current release
# serving unchanged; if the new candidate fails, traffic remains on current.
container_exists "$PREVIOUS_CONTAINER" && docker rm -f "$PREVIOUS_CONTAINER" >/dev/null
candidate_started=0
CANDIDATE_PROBE_DIR=""
cleanup_candidate() {
  if [ "$candidate_started" = 1 ]; then docker rm -f "$CANDIDATE_CONTAINER" >/dev/null 2>&1 || true; fi
  if [ -n "$CANDIDATE_PROBE_DIR" ]; then rm -rf "$CANDIDATE_PROBE_DIR" || true; fi
}
trap cleanup_candidate EXIT

if ! docker run -d --name "$CANDIDATE_CONTAINER" --restart always --network "$NETWORK" \
  -p "127.0.0.1:${candidate_port}:8080" "${APP_ENV[@]}" \
  -e "PUBLIC_URL=https://${DOMAIN}" \
  -e "DATABASE_URL=postgres://clubdue:${POSTGRES_PASSWORD}@${DB}:5432/clubdue?sslmode=disable" \
  -e "SESSION_SECRET=${SESSION_SECRET}" \
  -e RESEND_API_KEY -e INTEREST_NOTIFY_FROM -e INTEREST_NOTIFY_TO "$IMAGE" >/dev/null; then
  docker rm -f "$CANDIDATE_CONTAINER" >/dev/null 2>&1 || true
  fail "candidate ${CANDIDATE_CONTAINER} could not start; active release remains unchanged"
fi
candidate_started=1
wait_for_ready "$candidate_port" || fail "candidate ${CANDIDATE_CONTAINER} did not become ready; active release remains unchanged"
verify_candidate_contract "$candidate_port" "$DEPLOY_PUBLIC_DEMO"

# The active release stays available until the nginx cutover passes.
switch_proxy "$candidate_port"
if container_exists "$CURRENT_CONTAINER"; then docker rename "$CURRENT_CONTAINER" "$PREVIOUS_CONTAINER"; else docker rename "$CONTAINER" "$PREVIOUS_CONTAINER"; fi
docker rename "$CANDIDATE_CONTAINER" "$CURRENT_CONTAINER"
candidate_started=0
trap - EXIT
echo "==> Promoted ${CURRENT_CONTAINER} on 127.0.0.1:${candidate_port}; retained ${PREVIOUS_CONTAINER} on 127.0.0.1:${active_port} for rollback"
