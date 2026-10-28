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
    clubdue-staging:8082) ALT_PORT=18082; ENVIRONMENT=staging ;;
    # Pantry staging owns 127.0.0.1:18083. Keep ClubDue's rollback slot
    # isolated so a production candidate never competes for that listener.
    clubdue:8083) ALT_PORT=28083; ENVIRONMENT=production ;;
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

container_exists() { docker container inspect "$1" >/dev/null 2>&1; }

stop_and_remove_container() {
  local name="$1"
  container_exists "$name" || return 0
  if [ "$(docker inspect -f '{{.State.Running}}' "$name")" = true ]; then
    docker stop --timeout 110 "$name" >/dev/null
  fi
  docker rm "$name" >/dev/null
}

if [ "$CONTAINER" = clubdue-staging ]; then
  DEPLOY_PUBLIC_DEMO=1
else
  DEPLOY_PUBLIC_DEMO="${CLUBDUE_DEPLOY_PUBLIC_DEMO:-0}"
  case "$DEPLOY_PUBLIC_DEMO" in 0|1) ;; *) fail "CLUBDUE_DEPLOY_PUBLIC_DEMO must be 0 or 1" ;; esac
fi
readonly DEPLOY_PUBLIC_DEMO

if [ -n "${CLUBDUE_DEPLOY_TEST_PLAN:-}" ]; then
  if [ "$CLUBDUE_DEPLOY_TEST_PLAN" = cleanup ]; then
    stop_and_remove_container "${CONTAINER}-test-running"
  else
    rollout_plan
  fi
  exit 0
fi

DEPLOY_MODE="${CLUBDUE_DEPLOY_MODE:-promote}"
case "$DEPLOY_MODE" in promote|rollback) ;; *) fail "CLUBDUE_DEPLOY_MODE must be promote or rollback" ;; esac
if [ "${CLUBDUE_DEPLOY_VALIDATE_ONLY:-}" = "1" ]; then
  echo "ClubDue deployment target validated: ${CONTAINER} on ${PORT}; alternate ${ALT_PORT}"
  exit 0
fi

LOCK_ROOT="${SIDEYARD_LOCK_ROOT:-/run/sideyard-locks}"
if [ ! -e "$LOCK_ROOT" ]; then
  mkdir -m 700 "$LOCK_ROOT"
fi
[ -d "$LOCK_ROOT" ] && [ ! -L "$LOCK_ROOT" ] || fail "deployment lock root is missing or unsafe"
lock_mode="$(stat -c '%a' "$LOCK_ROOT")"
lock_owner="$(stat -c '%u:%g' "$LOCK_ROOT")"
[ "$lock_mode" = 700 ] || fail "deployment lock root must have mode 700"
if [ -z "${SIDEYARD_STATE_TEST_OWNER:-}" ]; then
  [ "$lock_owner" = 0:0 ] || fail "deployment lock root must be owned by root"
fi
exec 9>"${LOCK_ROOT}/sideyard-${ENVIRONMENT}-database-window.lock"
flock -w 120 9 || fail "another ${ENVIRONMENT} database or deployment operation is active"

: "${DOMAIN:?DOMAIN required}"
if [ "$DEPLOY_MODE" = promote ]; then
  : "${IMAGE:?IMAGE required}"
  : "${GHCR_USER:?GHCR_USER required}"
  : "${GHCR_TOKEN:?GHCR_TOKEN required}"
fi

SITE_PATH="${SIDEYARD_SITE_PATH:-/etc/nginx/sites-available/${DOMAIN}}"
ENABLED_PATH="${SIDEYARD_ENABLED_PATH:-/etc/nginx/sites-enabled/${DOMAIN}}"
CURRENT_CONTAINER="${CONTAINER}-current"
PREVIOUS_CONTAINER="${CONTAINER}-previous"
CANDIDATE_CONTAINER="${CONTAINER}-candidate"
STATE_DIR="${SIDEYARD_STATE_ROOT:-/var/lib/sideyard-operations}"
STATE_FILE="${STATE_DIR}/${ENVIRONMENT}-backend.env"
FINALIZATION_FILE="${STATE_DIR}/${ENVIRONMENT}-release-finalization.env"
STATE_VALIDATED=0

container_port() {
  local mapping
  mapping="$(docker port "$1" 8080/tcp 2>/dev/null || true)"
  # Docker omits runtime port mappings while the retained release is stopped.
  # Read its configured binding only in that state; running containers must
  # still expose the actual loopback listener. Multiple bindings fail below.
  if [ -z "$mapping" ] && [ "$(docker inspect -f '{{.State.Running}}' "$1")" = false ]; then
    mapping="$(docker inspect -f '{{range (index .HostConfig.PortBindings "8080/tcp")}}{{.HostIp}}:{{.HostPort}}{{println}}{{end}}' "$1")"
  fi
  case "$mapping" in
    "127.0.0.1:${PORT}"|"127.0.0.1:${ALT_PORT}") printf '%s\n' "${mapping##*:}" ;;
    *) fail "$1 does not expose a supported ClubDue loopback port" ;;
  esac
}

container_label() {
  docker inspect -f "{{ index .Config.Labels \"$2\" }}" "$1" 2>/dev/null || true
}

container_id() {
  docker inspect -f '{{.Id}}' "$1"
}

maybe_test_hard_kill() {
  local phase="$1"
  if [ -n "${SIDEYARD_STATE_TEST_OWNER:-}" ] && [ "${SIDEYARD_TEST_HARD_KILL_AFTER:-}" = "$phase" ]; then
    kill -KILL "$$"
  fi
}

container_name() {
  docker inspect -f '{{.Name}}' "$1"
}

# Normalize both release names using immutable container IDs. The temporary
# name makes this retryable after interruption at either rename.
normalize_release_names() {
  local target_id="$1" retained_id="$2" scratch="${CONTAINER}-finalizing"
  local target_name retained_name current_id previous_id scratch_id
  target_name="$(container_name "$target_id")"
  retained_name="$(container_name "$retained_id")"
  if [ "$target_name" = "/${CURRENT_CONTAINER}" ] && [ "$retained_name" = "/${PREVIOUS_CONTAINER}" ]; then
    return 0
  fi
  if container_exists "$scratch"; then
    scratch_id="$(container_id "$scratch")"
    [ "$scratch_id" = "$retained_id" ] || fail "release finalization scratch name belongs to another container"
  elif [ "$retained_name" != "/${PREVIOUS_CONTAINER}" ]; then
    docker rename "$retained_id" "$scratch"
    maybe_test_hard_kill retained-to-finalizing
  fi
  if container_exists "$CURRENT_CONTAINER"; then
    current_id="$(container_id "$CURRENT_CONTAINER")"
    [ "$current_id" = "$target_id" ] || fail "release finalization current name belongs to another container"
  else
    docker rename "$target_id" "$CURRENT_CONTAINER"
    maybe_test_hard_kill target-to-current
  fi
  if container_exists "$PREVIOUS_CONTAINER"; then
    previous_id="$(container_id "$PREVIOUS_CONTAINER")"
    [ "$previous_id" = "$retained_id" ] || fail "release finalization previous name belongs to another container"
  else
    docker rename "$retained_id" "$PREVIOUS_CONTAINER"
    maybe_test_hard_kill retained-to-previous
  fi
}

validate_backend_state() {
  local state_mode state_owner line_count
  [ -f "$STATE_FILE" ] && [ ! -L "$STATE_FILE" ] || fail "database backend state must be a regular file"
  state_mode="$(stat -c '%a' "$STATE_FILE")"
  state_owner="$(stat -c '%u:%g' "$STATE_FILE")"
  [ "$state_mode" = 600 ] || fail "database backend state must have mode 600"
  if [ -z "${SIDEYARD_STATE_TEST_OWNER:-}" ]; then
    [ "$state_owner" = 0:0 ] || fail "database backend state must be owned by root"
  fi
  line_count="$(wc -l < "$STATE_FILE")"
  [ "$line_count" = 2 ] || fail "database backend state must contain exactly two fields"
  SELECTED_BACKEND="$(sed -n 's/^DATABASE_BACKEND=//p' "$STATE_FILE")"
  SELECTED_GENERATION="$(sed -n 's/^DATA_GENERATION=//p' "$STATE_FILE")"
  [ "$(grep -c '^DATABASE_BACKEND=' "$STATE_FILE")" = 1 ] || fail "database backend state is invalid"
  [ "$(grep -c '^DATA_GENERATION=' "$STATE_FILE")" = 1 ] || fail "database backend state is invalid"
  case "$SELECTED_BACKEND" in postgres|azure_sql) ;; *) fail "database backend state is invalid" ;; esac
  [[ "$SELECTED_GENERATION" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$ ]] || fail "database generation is invalid"
  STATE_VALIDATED=1
  readonly SELECTED_BACKEND SELECTED_GENERATION
}

write_finalization_journal() {
  local operation="$1" target_id="$2" retained_id="$3" target_port="$4" journal_tmp
  mkdir -p "$STATE_DIR"
  journal_tmp="$(mktemp "${STATE_DIR}/.${ENVIRONMENT}-release-finalization.XXXXXX")"
  printf 'OPERATION=%s\nTARGET_ID=%s\nRETAINED_ID=%s\nTARGET_PORT=%s\n' \
    "$operation" "$target_id" "$retained_id" "$target_port" > "$journal_tmp"
  chmod 600 "$journal_tmp"
  if [ -z "${SIDEYARD_STATE_TEST_OWNER:-}" ]; then chown root:root "$journal_tmp"; fi
  sync -f "$journal_tmp"
  mv "$journal_tmp" "$FINALIZATION_FILE"
  sync -f "$STATE_DIR"
}

load_finalization_journal() {
  local journal_mode journal_owner line_count
  [ -f "$FINALIZATION_FILE" ] && [ ! -L "$FINALIZATION_FILE" ] || fail "release finalization journal is unsafe"
  journal_mode="$(stat -c '%a' "$FINALIZATION_FILE")"
  journal_owner="$(stat -c '%u:%g' "$FINALIZATION_FILE")"
  [ "$journal_mode" = 600 ] || fail "release finalization journal must have mode 600"
  if [ -z "${SIDEYARD_STATE_TEST_OWNER:-}" ]; then
    [ "$journal_owner" = 0:0 ] || fail "release finalization journal must be owned by root"
  fi
  line_count="$(wc -l < "$FINALIZATION_FILE")"
  [ "$line_count" = 4 ] || fail "release finalization journal must contain exactly four fields"
  FINALIZATION_OPERATION="$(sed -n 's/^OPERATION=//p' "$FINALIZATION_FILE")"
  FINALIZATION_TARGET_ID="$(sed -n 's/^TARGET_ID=//p' "$FINALIZATION_FILE")"
  FINALIZATION_RETAINED_ID="$(sed -n 's/^RETAINED_ID=//p' "$FINALIZATION_FILE")"
  FINALIZATION_TARGET_PORT="$(sed -n 's/^TARGET_PORT=//p' "$FINALIZATION_FILE")"
  [ "$(grep -c '^OPERATION=' "$FINALIZATION_FILE")" = 1 ] || fail "release finalization journal is invalid"
  [ "$(grep -c '^TARGET_ID=' "$FINALIZATION_FILE")" = 1 ] || fail "release finalization journal is invalid"
  [ "$(grep -c '^RETAINED_ID=' "$FINALIZATION_FILE")" = 1 ] || fail "release finalization journal is invalid"
  [ "$(grep -c '^TARGET_PORT=' "$FINALIZATION_FILE")" = 1 ] || fail "release finalization journal is invalid"
  case "$FINALIZATION_OPERATION" in promote|rollback) ;; *) fail "release finalization operation is invalid" ;; esac
  [[ "$FINALIZATION_TARGET_ID" =~ ^[A-Za-z0-9_.:-]+$ ]] || fail "release finalization target ID is invalid"
  [[ "$FINALIZATION_RETAINED_ID" =~ ^[A-Za-z0-9_.:-]+$ ]] || fail "release finalization retained ID is invalid"
  [ "$FINALIZATION_TARGET_ID" != "$FINALIZATION_RETAINED_ID" ] || fail "release finalization IDs must differ"
  [ "$FINALIZATION_TARGET_PORT" = "$PORT" ] || [ "$FINALIZATION_TARGET_PORT" = "$ALT_PORT" ] || fail "release finalization port is invalid"
}

complete_release_finalization() {
  local target_id="$1" retained_id="$2" target_port="$3"
  container_exists "$target_id" || fail "release finalization target is missing"
  container_exists "$retained_id" || fail "release finalization retained release is missing"
  container_matches_state "$target_id" || fail "release finalization target belongs to another data authority"
  container_matches_state "$retained_id" || fail "release finalization retained release belongs to another data authority"
  [ "$(container_port "$target_id")" = "$target_port" ] || fail "release finalization target port changed"
  docker update --restart=unless-stopped "$target_id" >/dev/null
  [ "$(docker inspect -f '{{.State.Running}}' "$target_id")" = true ] || docker start "$target_id" >/dev/null
  wait_for_ready "$target_port" || fail "release finalization target is not ready"
  switch_proxy "$target_port"
  docker update --restart=no "$retained_id" >/dev/null
  if [ "$(docker inspect -f '{{.State.Running}}' "$retained_id")" = true ]; then
    docker stop --timeout 110 "$retained_id" >/dev/null
  fi
  normalize_release_names "$target_id" "$retained_id"
  docker update --restart=unless-stopped "$target_id" >/dev/null
  docker update --restart=no "$retained_id" >/dev/null
  rm -f "$FINALIZATION_FILE"
}

resume_release_finalization() {
  [ -e "$FINALIZATION_FILE" ] || [ -L "$FINALIZATION_FILE" ] || return 0
  [ "$STATE_VALIDATED" = 1 ] || validate_backend_state
  load_finalization_journal
  complete_release_finalization "$FINALIZATION_TARGET_ID" "$FINALIZATION_RETAINED_ID" "$FINALIZATION_TARGET_PORT"
  echo "==> Resumed interrupted ${FINALIZATION_OPERATION} finalization"
}

marker_matches() {
  local marker="$1" expected_backend="$2" expected_generation="$3"
  [ "$marker" = "demo-sandbox-v2
environment=${ENVIRONMENT}
backend=${expected_backend}
generation=${expected_generation}" ]
}

container_matches_state() {
  local name="$1" backend generation
  backend="$(container_label "$name" sideyard.backend)"
  generation="$(container_label "$name" sideyard.generation)"
  if [ "$backend" = "$SELECTED_BACKEND" ] && [ "$generation" = "$SELECTED_GENERATION" ]; then
    return 0
  fi
  # Only pre-fence releases can lack labels, and Azure has never been active
  # before the first checked-in fence is created.
  [ "$SELECTED_BACKEND" = postgres ] && [ -z "$backend" ] && [ -z "$generation" ]
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
  # Keep the proxy envelope aligned with the app's bounded multipart requests.
  # Only our marked directive is replaced; nginx -t still rejects conflicts with
  # any independently managed directive and preserves the previous vhost.
  sed -i '/^[[:space:]]*client_max_body_size 8m; # sideyard-upload-limit$/d' "$candidate"
  sed -i '/# sideyard-request-envelope$/d' "$candidate"
  sed -E -i "/proxy_pass[[:space:]]+http:\/\/127\\.0\\.0\\.1:${target_port};/a\\        client_max_body_size 8m; # sideyard-upload-limit\\n        client_body_timeout 110s; # sideyard-request-envelope\\n        proxy_send_timeout 110s; # sideyard-request-envelope\\n        proxy_read_timeout 110s; # sideyard-request-envelope\\n        send_timeout 110s; # sideyard-request-envelope" "$candidate"
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
  local target_port="$1" deadline remaining
  deadline=$((SECONDS + 110))
  while [ "$SECONDS" -lt "$deadline" ]; do
    remaining=$((deadline - SECONDS))
    curl -fsS --max-time "$remaining" "http://127.0.0.1:${target_port}/readyz" 2>/dev/null | grep -qx ok && return 0
    sleep 1
  done
  return 1
}

verify_candidate_contract() {
  local target_port="$1" expected_demo="$2" demo_headers demo_body demo_status marker
  marker="$(curl -fsS --max-time 10 "http://127.0.0.1:${target_port}/rollback-safetyz" 2>/dev/null || true)"
  marker_matches "$marker" "$SELECTED_BACKEND" "$SELECTED_GENERATION" || fail "candidate does not match the authoritative database backend and generation; active release remains unchanged"

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
  session_status="$(curl -sS --max-time 110 -D "$session_headers" -o /dev/null -w '%{http_code}' -X POST \
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
  dashboard="$(curl -fsS --max-time 110 \
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

if [ -e "$FINALIZATION_FILE" ] || [ -L "$FINALIZATION_FILE" ]; then
  resume_release_finalization
  if [ "${SIDEYARD_DEPLOY_TEST_RECOVERY_ONLY:-0}" = 1 ] && [ -n "${SIDEYARD_STATE_TEST_OWNER:-}" ]; then
    exit 0
  fi
elif [ "${SIDEYARD_DEPLOY_TEST_RECOVERY_ONLY:-0}" = 1 ]; then
  fail "no interrupted release finalization is available"
fi

if container_exists "$CURRENT_CONTAINER"; then
  active_port="$(container_port "$CURRENT_CONTAINER")"
  active_container="$CURRENT_CONTAINER"
elif container_exists "$CONTAINER"; then
  # One-time migration from legacy stable container naming.
  active_port="$(container_port "$CONTAINER")"
  active_container="$CONTAINER"
else
  fail "no existing stable ${CONTAINER} release; provision and verify the ClubDue vhost before using reversible deploy"
fi
[ "$active_port" = "$PORT" ] || [ "$active_port" = "$ALT_PORT" ] || fail "unexpected active ClubDue port"
candidate_port="$ALT_PORT"; [ "$active_port" = "$ALT_PORT" ] && candidate_port="$PORT"
assert_safe_vhost
active_marker="$(curl -fsS "http://127.0.0.1:${active_port}/rollback-safetyz" 2>/dev/null || true)"

if [ -L "$STATE_FILE" ]; then
  fail "database backend state must not be a symbolic link"
fi
if [ ! -e "$STATE_FILE" ]; then
  [ "$active_marker" = "demo-sandbox-v1" ] || fail "database backend state is missing; refusing to infer it from a fenced release"
  mkdir -p "$STATE_DIR"
  chmod 700 "$STATE_DIR"
  state_tmp="$(mktemp "${STATE_DIR}/.${ENVIRONMENT}-backend.XXXXXX")"
  printf 'DATABASE_BACKEND=postgres\nDATA_GENERATION=%s\n' "$(cat /proc/sys/kernel/random/uuid)" > "$state_tmp"
  chmod 600 "$state_tmp"
  chown root:root "$state_tmp"
  mv "$state_tmp" "$STATE_FILE"
fi
[ "$STATE_VALIDATED" = 1 ] || validate_backend_state
if ! marker_matches "$active_marker" "$SELECTED_BACKEND" "$SELECTED_GENERATION"; then
  if [ "$SELECTED_BACKEND" != postgres ] || [ "$active_marker" != "demo-sandbox-v1" ] || ! container_matches_state "$active_container"; then
    fail "active release does not match the authoritative database backend and generation"
  fi
fi

if [ "$DEPLOY_MODE" = rollback ]; then
  container_exists "$CURRENT_CONTAINER" || fail "no current ClubDue release is available to roll back"
  container_exists "$PREVIOUS_CONTAINER" || fail "no retained previous ClubDue release is available"
  container_matches_state "$PREVIOUS_CONTAINER" || fail "retained release belongs to another database backend or generation"
  if container_exists "$CANDIDATE_CONTAINER"; then
    [ "$(docker inspect -f '{{.State.Running}}' "$CANDIDATE_CONTAINER")" != true ] || fail "running ClubDue candidate exists; inspect it before rollback"
    docker rm "$CANDIDATE_CONTAINER" >/dev/null
  fi
  current_port="$(container_port "$CURRENT_CONTAINER")"
  previous_port="$(container_port "$PREVIOUS_CONTAINER")"
  [ "$current_port" != "$previous_port" ] || fail "current and previous ClubDue releases share a port"
  docker update --restart=no "$PREVIOUS_CONTAINER" >/dev/null
  rollback_started=0
  rollback_cutover=0
  rollback_target_id="$(container_id "$PREVIOUS_CONTAINER")"
  rollback_retained_id="$(container_id "$CURRENT_CONTAINER")"
  cleanup_rollback() {
    result=$?
    trap - EXIT
    set +e
    if [ "$rollback_cutover" = 1 ]; then
      (complete_release_finalization "$rollback_target_id" "$rollback_retained_id" "$previous_port") >/dev/null 2>&1
      echo 'ERROR: rollback finalization failed after cutover; recovery kept the selected release running' >&2
    elif [ "$rollback_started" = 1 ]; then
      docker stop --timeout 110 "$rollback_target_id" >/dev/null 2>&1
      docker update --restart=no "$rollback_target_id" >/dev/null 2>&1
    fi
    exit "$result"
  }
  trap cleanup_rollback EXIT
  if [ "$(docker inspect -f '{{.State.Running}}' "$PREVIOUS_CONTAINER")" != true ]; then
    docker start "$PREVIOUS_CONTAINER" >/dev/null
  fi
  rollback_started=1
  [ "$(container_port "$PREVIOUS_CONTAINER")" = "$previous_port" ] || fail "retained release runtime port does not match its configured binding"
  wait_for_ready "$previous_port" || fail "retained previous ClubDue release is not ready"
  rollback_marker="$(curl -fsS "http://127.0.0.1:${previous_port}/rollback-safetyz" 2>/dev/null || true)"
  if ! marker_matches "$rollback_marker" "$SELECTED_BACKEND" "$SELECTED_GENERATION"; then
    [ "$SELECTED_BACKEND" = postgres ] && [ "$rollback_marker" = "demo-sandbox-v1" ] || fail "retained release runtime marker does not match authoritative data"
  fi
  write_finalization_journal rollback "$rollback_target_id" "$rollback_retained_id" "$previous_port"
  rollback_cutover=1
  complete_release_finalization "$rollback_target_id" "$rollback_retained_id" "$previous_port"
  rollback_started=0
  rollback_cutover=0
  trap - EXIT
  echo "==> Rolled back ${CONTAINER}: nginx now targets ${CURRENT_CONTAINER} on 127.0.0.1:${previous_port}"
  exit 0
fi

echo "==> ClubDue candidate deploy: ${CONTAINER} (${DOMAIN}) ← ${IMAGE}"
echo "$GHCR_TOKEN" | docker login ghcr.io -u "$GHCR_USER" --password-stdin
docker pull "$IMAGE"

if [ "$CONTAINER" = clubdue-staging ]; then
  ENV_FILE="${SIDEYARD_ENV_FILE:-/opt/clubdue/staging-db.env}"
  NETWORK=clubdue-staging-net; DB=clubdue-staging-db; VOLUME=clubdue-staging-pgdata
  mkdir -p "$(dirname "$ENV_FILE")"
  if [ ! -f "$ENV_FILE" ]; then
    umask 077
    { printf 'POSTGRES_PASSWORD=%s\n' "$(openssl rand -hex 24)"; printf 'SESSION_SECRET=%s\n' "$(openssl rand -hex 32)"; } > "$ENV_FILE"
  fi
  set -a; . "$ENV_FILE"; set +a
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
  APP_ENV=( -e APP_ENV=production -e "ENABLE_PUBLIC_DEMO=${DEPLOY_PUBLIC_DEMO}" )
fi

DATABASE_ENV=()
HOST_RUNTIME=()
if [ "$SELECTED_BACKEND" = postgres ]; then
  ensure_database "$NETWORK" "$DB" "$VOLUME"
  DATABASE_ENV=(
    -e DATABASE_BACKEND=postgres
    -e "DATABASE_URL=postgres://clubdue:${POSTGRES_PASSWORD}@${DB}:5432/clubdue?sslmode=disable"
    -e SCHEDULED_WORK_OWNER=embedded
  )
else
  [ "$ENVIRONMENT" = staging ] || fail "Azure SQL application runtime is not enabled for production"
  SQL_ENV_FILE="${SIDEYARD_SQL_ENV_FILE:-/opt/clubdue/staging-sql-runtime.env}"
  [ -f "$SQL_ENV_FILE" ] && [ ! -L "$SQL_ENV_FILE" ] || fail "staging SQL runtime credential file is missing or unsafe"
  sql_mode="$(stat -c '%a' "$SQL_ENV_FILE")"
  sql_owner="$(stat -c '%u:%g' "$SQL_ENV_FILE")"
  [ "$sql_mode" = 600 ] || fail "staging SQL runtime credential file must be root-owned mode 600"
  if [ -z "${SIDEYARD_STATE_TEST_OWNER:-}" ]; then
    [ "$sql_owner" = 0:0 ] || fail "staging SQL runtime credential file must be root-owned mode 600"
  fi
  grep -qx 'DATABASE_BACKEND=azure_sql' "$SQL_ENV_FILE" || fail "staging SQL runtime credential selects the wrong backend"
  grep -qx 'SCHEDULED_WORK_OWNER=host' "$SQL_ENV_FILE" || fail "staging SQL runtime must use host-owned scheduled work"
  grep -qx 'INTEREST_WAKE_FILE=/var/run/sideyard/interest.wake' "$SQL_ENV_FILE" || fail "staging SQL runtime has the wrong interest wake path"
  WAKE_ROOT="${SIDEYARD_WAKE_ROOT:-/run/sideyard}"
  mkdir -p "${WAKE_ROOT}/${ENVIRONMENT}"
  chmod 700 "${WAKE_ROOT}/${ENVIRONMENT}"
  docker network create "$NETWORK" >/dev/null 2>&1 || true
  DATABASE_ENV=( --env-file "$SQL_ENV_FILE" )
  HOST_RUNTIME=( -v "${WAKE_ROOT}/${ENVIRONMENT}:/var/run/sideyard" )
fi
APP_ENV+=( -e "DATA_GENERATION=${SELECTED_GENERATION}" )

azure_environment=production
[ "$CONTAINER" = clubdue-staging ] && azure_environment=staging
AZURE_ENV_FILE="/opt/clubdue/${azure_environment}-azure.env"
if [ -f "$AZURE_ENV_FILE" ]; then
  env_mode="$(stat -c '%a' "$AZURE_ENV_FILE")"
  [ $((8#$env_mode & 077)) -eq 0 ] || fail "Azure environment file must not be group/world readable"
  APP_ENV+=( --env-file "$AZURE_ENV_FILE" )
fi

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
stop_and_remove_container "$PREVIOUS_CONTAINER"
candidate_started=0
promotion_cutover=0
promotion_target_id=""
promotion_retained_id=""
CANDIDATE_PROBE_DIR=""
cleanup_candidate() {
  result=$?
  trap - EXIT
  set +e
  if [ "$promotion_cutover" = 1 ]; then
    (complete_release_finalization "$promotion_target_id" "$promotion_retained_id" "$candidate_port") >/dev/null 2>&1
    echo 'ERROR: deployment finalization failed after cutover; recovery kept the candidate release running' >&2
  elif [ "$candidate_started" = 1 ]; then
    docker update --restart=no "$CANDIDATE_CONTAINER" >/dev/null 2>&1
    stop_and_remove_container "$CANDIDATE_CONTAINER" >/dev/null 2>&1
  fi
  if [ -n "$CANDIDATE_PROBE_DIR" ]; then rm -rf "$CANDIDATE_PROBE_DIR" >/dev/null 2>&1; fi
  exit "$result"
}
trap cleanup_candidate EXIT

if ! docker run -d --name "$CANDIDATE_CONTAINER" --restart=no --stop-timeout 110 --network "$NETWORK" \
  --label "sideyard.environment=${ENVIRONMENT}" --label "sideyard.backend=${SELECTED_BACKEND}" \
  --label "sideyard.generation=${SELECTED_GENERATION}" \
  -p "127.0.0.1:${candidate_port}:8080" "${APP_ENV[@]}" "${DATABASE_ENV[@]}" "${HOST_RUNTIME[@]}" \
  -e "PUBLIC_URL=https://${DOMAIN}" \
  -e "SESSION_SECRET=${SESSION_SECRET}" \
  -e RESEND_API_KEY -e INTEREST_NOTIFY_FROM -e INTEREST_NOTIFY_TO "$IMAGE" >/dev/null; then
  stop_and_remove_container "$CANDIDATE_CONTAINER" >/dev/null 2>&1 || true
  fail "candidate ${CANDIDATE_CONTAINER} could not start; active release remains unchanged"
fi
candidate_started=1
promotion_target_id="$(container_id "$CANDIDATE_CONTAINER")"
promotion_retained_id="$(container_id "$active_container")"
wait_for_ready "$candidate_port" || fail "candidate ${CANDIDATE_CONTAINER} did not become ready; active release remains unchanged"
if [ "${SIDEYARD_DEPLOY_TEST_SKIP_CANDIDATE_CONTRACT:-0}" != 1 ] || [ -z "${SIDEYARD_STATE_TEST_OWNER:-}" ]; then
  verify_candidate_contract "$candidate_port" "$DEPLOY_PUBLIC_DEMO"
fi

# The journal is durable before any traffic or restart-policy change. A later
# invocation resumes this exact ID/port transition after process or host death.
write_finalization_journal promote "$promotion_target_id" "$promotion_retained_id" "$candidate_port"
promotion_cutover=1
complete_release_finalization "$promotion_target_id" "$promotion_retained_id" "$candidate_port"
if [ "$SELECTED_BACKEND" = azure_sql ]; then
  : > "${WAKE_ROOT}/${ENVIRONMENT}/interest.wake"
  chmod 600 "${WAKE_ROOT}/${ENVIRONMENT}/interest.wake"
fi
candidate_started=0
promotion_cutover=0
trap - EXIT
echo "==> Promoted ${CURRENT_CONTAINER} on 127.0.0.1:${candidate_port}; retained stopped ${PREVIOUS_CONTAINER} on 127.0.0.1:${active_port} for rollback"
