#!/usr/bin/env bash
# Exercise the production configuration, authenticated dashboard, encrypted
# backup, and restore path locally. It only creates resources in the explicit
# clubdue-production-rehearsal-* namespace and removes them on exit.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NETWORK="clubdue-production-rehearsal-net"
SOURCE_DB="clubdue-production-rehearsal-db"
RESTORE_DB="clubdue-production-rehearsal-restore-db"
APP="clubdue-production-rehearsal-app"
IMAGE="clubdue-production-rehearsal:local"
PORT="28083"
WORK_DIR="$(mktemp -d)"

cleanup() {
  docker rm -f "$APP" "$SOURCE_DB" "$RESTORE_DB" >/dev/null 2>&1 || true
  docker network rm "$NETWORK" >/dev/null 2>&1 || true
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT

python3 "$ROOT/scripts/test-backup-auth.py"
bash "$ROOT/scripts/azure/test-runtime-operations.sh"

# Keep every outer transport beyond the Azure request deadline and keep the
# container stop grace beyond the application's shutdown window.
grep -q -- '--stop-timeout 110' "$ROOT/scripts/droplet-deploy.sh"
grep -q 'deadline=$((SECONDS + 110))' "$ROOT/scripts/droplet-deploy.sh"
grep -q 'proxy_read_timeout 110s; # sideyard-request-envelope' "$ROOT/scripts/droplet-deploy.sh"
grep -q 'proxy_send_timeout 110s; # sideyard-request-envelope' "$ROOT/scripts/droplet-deploy.sh"
grep -q 'client_body_timeout 110s; # sideyard-request-envelope' "$ROOT/scripts/droplet-deploy.sh"
grep -q 'curl() { command curl --max-time 110 "$@"; }' "$ROOT/scripts/smoke-test.sh"
grep -q 'setTimeout(() => controller.abort(), 110000)' "$ROOT/internal/app/static/visits.js"
grep -q 'proxy_read_timeout 110s; # sideyard-request-envelope' "$ROOT/scripts/nginx/sideyard.conf"

# These validation checks run inside a disposable Docker shell. The helper's
# narrow plan seam exercises rollout state selection without a droplet, nginx,
# registry access, or a way to mutate real deployment state.
DEPLOY_TEST_IMAGE="ubuntu:24.04"
run_deploy_helper() {
  docker run --rm --network none \
    -v "$ROOT/scripts/droplet-deploy.sh:/opt/clubdue/droplet-deploy.sh:ro" \
    -e CONTAINER="$1" -e PORT="$2" "${@:3}" \
    "$DEPLOY_TEST_IMAGE" bash /opt/clubdue/droplet-deploy.sh
}

for deployment_target in 'clubdue:8080' 'clubdue:8081' 'clubdue:8082' 'clubdue-staging:8083'; do
  deployment_container="${deployment_target%%:*}"
  deployment_port="${deployment_target##*:}"
  if run_deploy_helper "$deployment_container" "$deployment_port" >/dev/null 2>&1; then
    echo "invalid deployment target ${deployment_target} was accepted" >&2
    exit 1
  fi
done
run_deploy_helper clubdue-staging 8082 -e CLUBDUE_DEPLOY_VALIDATE_ONLY=1 >/dev/null
if run_deploy_helper clubdue 8083 -e CLUBDUE_DEPLOY_PUBLIC_DEMO=2 -e CLUBDUE_DEPLOY_TEST_PLAN=candidate >/dev/null 2>&1; then
  echo "invalid production public-demo setting was accepted" >&2
  exit 1
fi

[ "$(run_deploy_helper clubdue 8083 -e CLUBDUE_DEPLOY_TEST_PLAN=candidate)" = \
  'candidate=28083 active=8083 failure=preserve-active public-demo=0' ]
[ "$(run_deploy_helper clubdue 8083 -e ENABLE_PUBLIC_DEMO=1 -e CLUBDUE_DEPLOY_PUBLIC_DEMO=0 -e CLUBDUE_DEPLOY_TEST_PLAN=candidate)" = \
  'candidate=28083 active=8083 failure=preserve-active public-demo=0' ]
[ "$(run_deploy_helper clubdue 8083 -e CLUBDUE_DEPLOY_PUBLIC_DEMO=1 -e CLUBDUE_DEPLOY_TEST_PLAN=candidate)" = \
  'candidate=28083 active=8083 failure=preserve-active public-demo=1' ]
[ "$(run_deploy_helper clubdue-staging 8082 -e CLUBDUE_DEPLOY_PUBLIC_DEMO=0 -e CLUBDUE_DEPLOY_TEST_PLAN=candidate)" = \
  'candidate=18082 active=8082 failure=preserve-active public-demo=1' ]
[ "$(run_deploy_helper clubdue 8083 -e CLUBDUE_DEPLOY_TEST_PLAN=promote)" = \
  'proxy=28083 active=28083 previous=8083' ]
[ "$(run_deploy_helper clubdue 8083 -e CLUBDUE_DEPLOY_TEST_PLAN=next-promote)" = \
  'release-previous=8083 candidate=8083 active=8083 previous=28083' ]
[ "$(run_deploy_helper clubdue 8083 -e CLUBDUE_DEPLOY_TEST_PLAN=rollback)" = \
  'proxy=8083 active=8083 previous=28083' ]

deploy_mock="$WORK_DIR/deploy-mock"
mkdir -p "$deploy_mock"
cat > "$deploy_mock/docker" <<'SH'
#!/usr/bin/env sh
printf '%s\n' "$*" >> "$CLUBDUE_DEPLOY_TEST_DOCKER_LOG"
case "$1 $2" in
  'container inspect') exit 0 ;;
  'inspect -f') printf 'true\n'; exit 0 ;;
  'stop --timeout') exit 0 ;;
  'rm clubdue-staging-test-running') exit 0 ;;
esac
exit 1
SH
chmod 755 "$deploy_mock/docker"
chmod 777 "$deploy_mock"
deploy_log="$deploy_mock/deploy-docker.log"
: > "$deploy_log"
chmod 666 "$deploy_log"
run_deploy_helper clubdue-staging 8082 \
  -v "$deploy_mock/docker:/usr/local/bin/docker:ro" \
  -e CLUBDUE_DEPLOY_TEST_DOCKER_LOG=/test-output/deploy-docker.log \
  -v "$deploy_mock:/test-output" \
  -e CLUBDUE_DEPLOY_TEST_PLAN=cleanup >/dev/null
grep -Fx 'stop --timeout 110 clubdue-staging-test-running' "$deploy_log"
grep -Fx 'rm clubdue-staging-test-running' "$deploy_log"
if grep -Eq 'rm .*-[fF]($| )' "$deploy_log"; then
  echo 'deploy cleanup used forced removal' >&2
  exit 1
fi

# Apply the same managed-line replacement as switch_proxy to a fresh template,
# then let nginx reject duplicate or invalid timeout directives.
nginx_candidate="$WORK_DIR/sideyard.conf"
cp "$ROOT/scripts/nginx/sideyard.conf" "$nginx_candidate"
sed -i 's/__CLUBDUE_PORT__/8083/' "$nginx_candidate"
sed -i '/^[[:space:]]*client_max_body_size 8m; # sideyard-upload-limit$/d' "$nginx_candidate"
sed -i '/# sideyard-request-envelope$/d' "$nginx_candidate"
sed -E -i '/proxy_pass[[:space:]]+http:\/\/127\.0\.0\.1:8083;/a\        client_max_body_size 8m; # sideyard-upload-limit\n        client_body_timeout 110s; # sideyard-request-envelope\n        proxy_send_timeout 110s; # sideyard-request-envelope\n        proxy_read_timeout 110s; # sideyard-request-envelope\n        send_timeout 110s; # sideyard-request-envelope' "$nginx_candidate"
for directive in client_body_timeout proxy_send_timeout proxy_read_timeout send_timeout; do
  [ "$(grep -c "^[[:space:]]*${directive} 110s; # sideyard-request-envelope$" "$nginx_candidate")" = 1 ]
done
sed -E -i 's/listen 80;/listen 8081;/; s/listen 443 ssl;/listen 8080;/; /^ *ssl_(certificate|certificate_key|dhparam) /d; /^ *include \/etc\/letsencrypt\//d' "$nginx_candidate"
docker run --rm -v "$nginx_candidate:/etc/nginx/conf.d/default.conf:ro" nginx:1.27-alpine nginx -t >/dev/null

wait_for_postgres() {
  local container="$1"
  for _ in $(seq 1 30); do
    if docker exec "$container" pg_isready -U clubdue -d clubdue >/dev/null 2>&1; then
      # The official Postgres image briefly accepts connections during its
      # initialization server, then restarts for the final server. Require a
      # second successful probe after that handoff before backup or restore.
      sleep 2
      if docker exec "$container" psql --username=clubdue --dbname=clubdue --command 'SELECT 1' >/dev/null 2>&1; then
        return 0
      fi
    fi
    sleep 1
  done
  echo "Postgres rehearsal container did not become ready" >&2
  return 1
}

wait_for_http() {
  for _ in $(seq 1 30); do
    if curl -fsS "http://127.0.0.1:${PORT}/readyz" 2>/dev/null | grep -qx ok; then
      return 0
    fi
    sleep 1
  done
  echo "production rehearsal app did not become ready" >&2
  return 1
}

cookie_value() {
  sed -n "s/^Set-Cookie: $1=\([^;]*\).*/\1/pI" "$2" | tail -1
}

docker network create "$NETWORK" >/dev/null
docker run -d --name "$SOURCE_DB" --network "$NETWORK" \
  -e POSTGRES_USER=clubdue -e POSTGRES_PASSWORD=rehearsal-db-password \
  -e POSTGRES_DB=clubdue postgres:16-alpine >/dev/null
wait_for_postgres "$SOURCE_DB"

docker build -t "$IMAGE" "$ROOT" >/dev/null
docker run -d --name "$APP" --network "$NETWORK" -p "127.0.0.1:${PORT}:8080" \
  -e APP_ENV=production \
  -e ENABLE_PUBLIC_DEMO=1 \
  -e 'PUBLIC_URL=https://clubdue-production-rehearsal.invalid' \
  -e 'DATABASE_URL=postgres://clubdue:rehearsal-db-password@clubdue-production-rehearsal-db:5432/clubdue?sslmode=disable' \
  -e 'SESSION_SECRET=production-rehearsal-session-secret-0123456789' \
  -e 'BOOTSTRAP_EMAIL=owner@rehearsal.invalid' \
  -e 'BOOTSTRAP_PASSWORD=production-rehearsal-password' \
  -e 'BOOTSTRAP_SHOP_NAME=Production Rehearsal HVAC' \
  "$IMAGE" >/dev/null
wait_for_http

login_html="$WORK_DIR/login.html"
login_headers="$WORK_DIR/login.headers"
login_result_headers="$WORK_DIR/login-result.headers"
curl -fsS -D "$login_headers" -o "$login_html" "http://127.0.0.1:${PORT}/login"
csrf_token="$(sed -n 's/.*name="csrf_token" value="\([^"]*\)".*/\1/p' "$login_html" | head -1)"
csrf_cookie="$(cookie_value clubdue_csrf "$login_headers")"
[ -n "$csrf_token" ] && [ -n "$csrf_cookie" ]

login_status="$(curl -sS -D "$login_result_headers" -o /dev/null -w '%{http_code}' -X POST \
  -H "Cookie: clubdue_csrf=${csrf_cookie}" \
  --data-urlencode "csrf_token=${csrf_token}" \
  --data-urlencode 'email=owner@rehearsal.invalid' \
  --data-urlencode 'password=production-rehearsal-password' \
  "http://127.0.0.1:${PORT}/login")"
[ "$login_status" = "303" ]
session_cookie="$(cookie_value clubdue_session "$login_result_headers")"
[ -n "$session_cookie" ]

dashboard="$(curl -fsS -H "Cookie: clubdue_csrf=${csrf_cookie}; clubdue_session=${session_cookie}" "http://127.0.0.1:${PORT}/dashboard")"
printf '%s' "$dashboard" | grep -q 'Dashboard — Sideyard'

# Deployment smoke checks log in as the configured owner. That additional login
# must not invalidate a user's existing browser session for the same account.
second_login_headers="$WORK_DIR/second-login.headers"
second_login_status="$(curl -sS -D "$second_login_headers" -o /dev/null -w '%{http_code}' -X POST \
  -H "Cookie: clubdue_csrf=${csrf_cookie}" \
  --data-urlencode "csrf_token=${csrf_token}" \
  --data-urlencode 'email=owner@rehearsal.invalid' \
  --data-urlencode 'password=production-rehearsal-password' \
  "http://127.0.0.1:${PORT}/login")"
[ "$second_login_status" = "303" ]
second_session_cookie="$(cookie_value clubdue_session "$second_login_headers")"
[ -n "$second_session_cookie" ] && [ "$second_session_cookie" != "$session_cookie" ]
dashboard_after_second_login="$(curl -fsS -H "Cookie: clubdue_csrf=${csrf_cookie}; clubdue_session=${session_cookie}" "http://127.0.0.1:${PORT}/dashboard")"
printf '%s' "$dashboard_after_second_login" | grep -q 'Dashboard — Sideyard'
second_dashboard="$(curl -fsS -H "Cookie: clubdue_csrf=${csrf_cookie}; clubdue_session=${second_session_cookie}" "http://127.0.0.1:${PORT}/dashboard")"
printf '%s' "$second_dashboard" | grep -q 'Dashboard — Sideyard'

dev_route_status="$(curl -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:${PORT}/dev/seed")"
[ "$dev_route_status" = "404" ]
rollback_marker="$(curl -fsS "http://127.0.0.1:${PORT}/rollback-safetyz")"
[ "${rollback_marker%%$'\n'*}" = demo-sandbox-v2 ]

demo_html="$WORK_DIR/demo.html"
demo_headers="$WORK_DIR/demo.headers"
demo_session_headers="$WORK_DIR/demo-session.headers"
curl -fsS -D "$demo_headers" -o "$demo_html" "http://127.0.0.1:${PORT}/demo"
grep -q 'action="/demo/session"' "$demo_html"
grep -Eiq '^Cache-Control:.*no-store' "$demo_headers"
grep -Eiq '^X-Robots-Tag:.*noindex' "$demo_headers"
if grep -Eiq '^Set-Cookie:[[:space:]]*clubdue_session=' "$demo_headers"; then
  echo "production demo entry created a session before consent" >&2
  exit 1
fi
demo_csrf_token="$(sed -n 's/.*name="csrf_token" value="\([^"]*\)".*/\1/p' "$demo_html" | head -1)"
demo_csrf_cookie="$(cookie_value clubdue_csrf "$demo_headers")"
[ -n "$demo_csrf_token" ] && [ "$demo_csrf_token" = "$demo_csrf_cookie" ]
grep -Eiq '^Set-Cookie:[[:space:]]*clubdue_csrf=.*Secure' "$demo_headers"
grep -Eiq '^Set-Cookie:[[:space:]]*clubdue_csrf=.*HttpOnly' "$demo_headers"
grep -Eiq '^Set-Cookie:[[:space:]]*clubdue_csrf=.*SameSite=Lax' "$demo_headers"

demo_session_status="$(curl -sS --max-time 20 -D "$demo_session_headers" -o /dev/null -w '%{http_code}' -X POST \
  -H "Cookie: clubdue_csrf=${demo_csrf_cookie}" \
  --data-urlencode "csrf_token=${demo_csrf_token}" \
  "http://127.0.0.1:${PORT}/demo/session")"
[ "$demo_session_status" = "303" ]
demo_session_cookie="$(cookie_value clubdue_session "$demo_session_headers")"
[ -n "$demo_session_cookie" ]
grep -Eiq '^Set-Cookie:[[:space:]]*clubdue_session=.*Secure' "$demo_session_headers"
grep -Eiq '^Set-Cookie:[[:space:]]*clubdue_session=.*HttpOnly' "$demo_session_headers"
grep -Eiq '^Set-Cookie:[[:space:]]*clubdue_session=.*SameSite=Lax' "$demo_session_headers"

demo_dashboard="$(curl -fsS -H "Cookie: clubdue_csrf=${demo_csrf_cookie}; clubdue_session=${demo_session_cookie}" "http://127.0.0.1:${PORT}/dashboard")"
printf '%s' "$demo_dashboard" | grep -q 'Evergreen Lawn &amp; Landscape'
printf '%s' "$demo_dashboard" | grep -q 'Fictional public demo'
demo_write_status="$(curl -sS -o /dev/null -w '%{http_code}' -X POST \
  -H "Cookie: clubdue_csrf=${demo_csrf_cookie}; clubdue_session=${demo_session_cookie}" \
  --data-urlencode "csrf_token=${demo_csrf_token}" \
  --data-urlencode 'name=Production Rehearsal Garden Client' \
  --data-urlencode 'email=production-rehearsal-demo@example.test' \
  --data-urlencode 'phone=555-0199' \
  "http://127.0.0.1:${PORT}/customers")"
[ "$demo_write_status" = "303" ]
demo_customers="$(curl -fsS -H "Cookie: clubdue_csrf=${demo_csrf_cookie}; clubdue_session=${demo_session_cookie}" "http://127.0.0.1:${PORT}/customers")"
printf '%s' "$demo_customers" | grep -q 'Production Rehearsal Garden Client'

owner_dashboard_after_demo="$(curl -fsS -H "Cookie: clubdue_csrf=${csrf_cookie}; clubdue_session=${session_cookie}" "http://127.0.0.1:${PORT}/dashboard")"
printf '%s' "$owner_dashboard_after_demo" | grep -q 'Dashboard — Sideyard'
if printf '%s' "$owner_dashboard_after_demo" | grep -q 'Fictional public demo'; then
  echo "production demo traffic crossed into the real owner session" >&2
  exit 1
fi

export CLUBDUE_BACKUP_DB_CONTAINER="$SOURCE_DB"
# Exercise the real private upload path before backup. Bytes and metadata must
# restore together, and a retried multipart submission must not create duplicates.
photo_property_id="$(docker exec "$SOURCE_DB" psql -U clubdue -d clubdue -qAt -v ON_ERROR_STOP=1 -c "
  WITH c AS (INSERT INTO customers(shop_id,name)
    SELECT shop_id,'Photo rehearsal customer' FROM users WHERE email='owner@rehearsal.invalid' RETURNING id,shop_id)
  INSERT INTO properties(shop_id,customer_id,address_line1,city,state,postal_code)
  SELECT shop_id,id,'12 Photo Street','Cary','NC','27511' FROM c RETURNING id")"
[ -n "$photo_property_id" ]
cp "$ROOT/internal/app/static/sideyard-icon-32-v2.png" "$WORK_DIR/photo-upload.png"
# Trailing bytes force the real scratch image's multipart temp-file path. Image
# normalization discards them; a missing /tmp must fail this release rehearsal.
dd if=/dev/zero bs=1048576 count=2 >> "$WORK_DIR/photo-upload.png" 2>/dev/null
for _ in 1 2; do
  photo_upload_status="$(curl -sS -o "$WORK_DIR/photo-upload.html" -w '%{http_code}' \
    -H "Cookie: clubdue_csrf=${csrf_cookie}; clubdue_session=${session_cookie}" \
    -F "csrf_token=${csrf_token}" -F 'photo_key=11111111111111111111111111111111' \
    -F 'caption=Production rehearsal property photo' -F 'category=before' \
    -F "photo=@${WORK_DIR}/photo-upload.png;type=image/png" \
    "http://127.0.0.1:${PORT}/properties/${photo_property_id}/photos")"
  [ "$photo_upload_status" = "303" ]
done
photo_count="$(docker exec "$SOURCE_DB" psql -U clubdue -d clubdue -qAt -c "SELECT count(*) FROM property_photos WHERE property_id='${photo_property_id}'")"
[ "$photo_count" = "1" ]
photo_id="$(docker exec "$SOURCE_DB" psql -U clubdue -d clubdue -qAt -c "SELECT id FROM property_photos WHERE property_id='${photo_property_id}'")"
photo_fingerprint="$(docker exec "$SOURCE_DB" psql -U clubdue -d clubdue -qAt -c "SELECT md5(image_data)||':'||caption||':'||recorded_by FROM property_photos WHERE id='${photo_id}'")"
curl -fsS -H "Cookie: clubdue_csrf=${csrf_cookie}; clubdue_session=${session_cookie}" \
  "http://127.0.0.1:${PORT}/property-photos/${photo_id}" -o "$WORK_DIR/private-photo.png"
[ -s "$WORK_DIR/private-photo.png" ]
photo_anonymous_status="$(curl -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:${PORT}/property-photos/${photo_id}")"
[ "$photo_anonymous_status" = "303" ]
# The scratch image must resolve named timezones without host zoneinfo files.
history_equipment_id="$(docker exec "$SOURCE_DB" psql -U clubdue -d clubdue -qAt -v ON_ERROR_STOP=1 -c "INSERT INTO equipment(shop_id,property_id,kind) SELECT shop_id,id,'Rehearsal heat pump' FROM properties WHERE id='${photo_property_id}' RETURNING id")"
history_status="$(curl -sS -o "$WORK_DIR/history-save.html" -w '%{http_code}' \
  -H "Cookie: clubdue_csrf=${csrf_cookie}; clubdue_session=${session_cookie}" \
  --data-urlencode "csrf_token=${csrf_token}" \
  --data-urlencode 'note_key=33333333333333333333333333333333' \
  --data-urlencode 'note_body=Rehearsal filter service' --data-urlencode 'occurred_on=2025-11-02' \
  --data-urlencode 'event_time=01:30' --data-urlencode 'event_timezone=America/New_York' \
  --data-urlencode 'event_offset=-04:00' \
  "http://127.0.0.1:${PORT}/equipment/${history_equipment_id}/history")"
[ "$history_status" = "303" ]
visit_history_id="$(docker exec "$SOURCE_DB" psql -U clubdue -d clubdue -qAt -v ON_ERROR_STOP=1 -c "
  WITH a AS (INSERT INTO agreements(shop_id,customer_id,property_id,anniversary_date)
    SELECT shop_id,customer_id,id,'2027-01-01' FROM properties WHERE id='${photo_property_id}' RETURNING id,shop_id)
  INSERT INTO visit_dues(shop_id,agreement_id,season,due_year)
    SELECT shop_id,id,'fall',2026 FROM a RETURNING id")"
for _ in 1 2; do
  visit_history_status="$(curl -sS -o "$WORK_DIR/visit-history-save.html" -w '%{http_code}' \
    -H "Cookie: clubdue_csrf=${csrf_cookie}; clubdue_session=${session_cookie}" \
    --data-urlencode "csrf_token=${csrf_token}" \
    --data-urlencode 'note_key=44444444444444444444444444444444' \
    --data-urlencode 'note_body=Private visit inspection' --data-urlencode 'occurred_on=2025-11-02' \
    --data-urlencode 'event_time=01:30' --data-urlencode 'event_timezone=America/New_York' \
    --data-urlencode 'event_offset=-05:00' \
    "http://127.0.0.1:${PORT}/visits/${visit_history_id}/history")"
  [ "$visit_history_status" = "303" ]
done
job_customer_id="$(docker exec "$SOURCE_DB" psql -U clubdue -d clubdue -qAt -c "SELECT customer_id FROM properties WHERE id='${photo_property_id}'")"
for _ in 1 2; do
  job_status="$(curl -sS -o "$WORK_DIR/job-create.html" -w '%{http_code}' \
    -H "Cookie: clubdue_csrf=${csrf_cookie}; clubdue_session=${session_cookie}" \
    --data-urlencode "csrf_token=${csrf_token}" --data-urlencode "customer_id=${job_customer_id}" \
    --data-urlencode "property_id=${photo_property_id}" --data-urlencode 'job_key=55555555555555555555555555555555' \
    --data-urlencode 'kind=survey' --data-urlencode 'title=Rehearsal site survey' \
    --data-urlencode 'details=Inspect access before quoting' "http://127.0.0.1:${PORT}/jobs")"
  [ "$job_status" = "303" ]
done
job_id="$(docker exec "$SOURCE_DB" psql -U clubdue -d clubdue -qAt -c "SELECT id FROM jobs WHERE customer_id='${job_customer_id}' AND submission_key='55555555555555555555555555555555'")"
job_outcome_status="$(curl -sS -o "$WORK_DIR/job-outcome.html" -w '%{http_code}' \
  -H "Cookie: clubdue_csrf=${csrf_cookie}; clubdue_session=${session_cookie}" \
  --data-urlencode "csrf_token=${csrf_token}" --data-urlencode 'revision=1' --data-urlencode 'action=completed' \
  "http://127.0.0.1:${PORT}/jobs/${job_id}")"
[ "$job_outcome_status" = "303" ]
export CLUBDUE_BACKUP_DIR="$WORK_DIR/backups"
export CLUBDUE_BACKUP_PASSPHRASE="disposable-production-rehearsal-backup-passphrase"
export CLUBDUE_BACKUP_HMAC_KEY="disposable-production-rehearsal-hmac-key"
export CLUBDUE_BACKUP_RETENTION_DAYS=30
mkdir -p "$CLUBDUE_BACKUP_DIR"
expired_backup="$CLUBDUE_BACKUP_DIR/clubdue-postgres-20000101T000000Z.sql.enc"
expired_backup_mac="${expired_backup}.mac"
touch -d '2000-01-01 UTC' "$expired_backup"
touch -d '2000-01-01 UTC' "$expired_backup_mac"
backup_file="$("$ROOT/scripts/postgres-backup.sh")"
backup_mac_file="${backup_file}.mac"
[ -s "$backup_file" ]
[ -s "$backup_mac_file" ]
[ ! -e "$expired_backup" ]
[ ! -e "$expired_backup_mac" ]
[ "$(stat -c '%a' "$backup_file")" = "600" ]
[ "$(stat -c '%a' "$backup_mac_file")" = "600" ]
[ "$(stat -c '%s' "$backup_mac_file")" = "39" ]
# Drain the plaintext: grep -q closes early and can make OpenSSL fail with a
# broken pipe even after a valid owner match. pipefail still checks decryption.
if openssl enc -d -aes-256-cbc -pbkdf2 -pass env:CLUBDUE_BACKUP_PASSPHRASE -in "$backup_file" | grep -a 'owner@rehearsal.invalid' >/dev/null; then
  :
else
  echo "backup did not contain the rehearsal owner after decryption" >&2
  exit 1
fi
if grep -aq 'owner@rehearsal.invalid' "$backup_file"; then
  echo "backup was not encrypted" >&2
  exit 1
fi

docker run -d --name "$RESTORE_DB" --network "$NETWORK" \
  -e POSTGRES_USER=clubdue -e POSTGRES_PASSWORD=rehearsal-restore-password \
  -e POSTGRES_DB=clubdue postgres:16-alpine >/dev/null
wait_for_postgres "$RESTORE_DB"
export CLUBDUE_RESTORE_DB_CONTAINER="$RESTORE_DB"
"$ROOT/scripts/postgres-restore.sh" "$backup_file"
restored_owner_count="$(docker exec "$RESTORE_DB" psql --username=clubdue --dbname=clubdue --tuples-only --no-align \
  --command "SELECT count(*) FROM users WHERE email = 'owner@rehearsal.invalid'")"
[ "$restored_owner_count" = "1" ]
restored_photo_fingerprint="$(docker exec "$RESTORE_DB" psql -U clubdue -d clubdue -qAt -c "SELECT md5(image_data)||':'||caption||':'||recorded_by FROM property_photos WHERE id='${photo_id}'")"
[ "$restored_photo_fingerprint" = "$photo_fingerprint" ]
restored_history_count="$(docker exec "$RESTORE_DB" psql -U clubdue -d clubdue -qAt -c "SELECT count(*) FROM asset_notes WHERE equipment_id='${history_equipment_id}' AND body='Rehearsal filter service' AND occurred_at='2025-11-02T05:30:00Z'::timestamptz AND event_timezone='America/New_York' AND recorded_by='owner@rehearsal.invalid'")"
[ "$restored_history_count" = "1" ]
restored_visit_history_count="$(docker exec "$RESTORE_DB" psql -U clubdue -d clubdue -qAt -c "SELECT count(*) FROM visit_notes n JOIN visit_dues v ON v.id=n.seasonal_visit_id JOIN agreements a ON a.id=v.agreement_id WHERE v.id='${visit_history_id}' AND n.body='Private visit inspection' AND n.occurred_at='2025-11-02T06:30:00Z'::timestamptz AND n.event_timezone='America/New_York' AND n.recorded_by='owner@rehearsal.invalid' AND v.status='due' AND a.visits_used=0")"
[ "$restored_visit_history_count" = "1" ]
restored_job_count="$(docker exec "$RESTORE_DB" psql -U clubdue -d clubdue -qAt -c "SELECT count(*) FROM jobs WHERE id='${job_id}' AND title='Rehearsal site survey' AND status='completed' AND completed_at IS NOT NULL AND scheduled_on IS NULL AND revision=2")"
[ "$restored_job_count" = "1" ]
restored_job_events="$(docker exec "$RESTORE_DB" psql -U clubdue -d clubdue -qAt -c "SELECT count(*) FROM job_events WHERE job_id='${job_id}' AND recorded_by='owner@rehearsal.invalid' AND snapshot->>'Details'='Inspect access before quoting'")"
[ "$restored_job_events" = "2" ]

expect_authenticated_restore_failure() {
  local candidate_backup="$1"
  local failure_output="$WORK_DIR/restore-failure.log"

  if "$ROOT/scripts/postgres-restore.sh" "$candidate_backup" > /dev/null 2>"$failure_output"; then
    echo "tampered backup was restored" >&2
    exit 1
  fi
  grep -qx 'backup authentication failed' "$failure_output"
}

tampered_ciphertext="$WORK_DIR/tampered-ciphertext.sql.enc"
cp "$backup_file" "$tampered_ciphertext"
cp "$backup_mac_file" "${tampered_ciphertext}.mac"
# Appending a byte guarantees a different authenticated ciphertext while
# keeping the original valid MAC sidecar intact.
printf '\000' >> "$tampered_ciphertext"
expect_authenticated_restore_failure "$tampered_ciphertext"

tampered_mac="$WORK_DIR/tampered-mac.sql.enc"
cp "$backup_file" "$tampered_mac"
cp "$backup_mac_file" "${tampered_mac}.mac"
first_mac_byte="$(od -An -tu1 -N1 "${tampered_mac}.mac" | tr -d '[:space:]')"
if [ "$first_mac_byte" = "0" ]; then
  replacement_mac_byte=1
else
  replacement_mac_byte=0
fi
printf "\\$(printf '%03o' "$replacement_mac_byte")" \
  | dd of="${tampered_mac}.mac" bs=1 count=1 conv=notrunc status=none
expect_authenticated_restore_failure "$tampered_mac"

CLUBDUE_BACKUP_HMAC_KEY="different-disposable-rehearsal-key" \
  expect_authenticated_restore_failure "$backup_file"

restored_owner_count_after_tamper_tests="$(docker exec "$RESTORE_DB" psql --username=clubdue --dbname=clubdue --tuples-only --no-align \
  --command "SELECT count(*) FROM users WHERE email = 'owner@rehearsal.invalid'")"
[ "$restored_owner_count_after_tamper_tests" = "1" ]

echo "production rehearsal passed: public demo isolation/write path, owner sessions, authenticated encrypted backup, restore, and tamper rejection"
