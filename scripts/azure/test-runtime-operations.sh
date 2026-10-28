#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
cleanup() {
  if [ "${SIDEYARD_TEST_KEEP_WORK:-0}" = 1 ]; then echo "test work retained at $WORK" >&2; return; fi
  find "$WORK" -type f -delete
  find "$WORK" -depth -type d -empty -delete
}
trap cleanup EXIT
mkdir -p "$WORK/state" "$WORK/lock" "$WORK/wake/staging" "$WORK/bin"
chmod 700 "$WORK/lock"
generation=aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa
state="$WORK/state/staging-backend.env"
printf 'DATABASE_BACKEND=azure_sql\nDATA_GENERATION=%s\n' "$generation" > "$state"
chmod 600 "$state"

SIDEYARD_STATE_ROOT="$WORK/state" SIDEYARD_STATE_TEST_OWNER=1 bash -c '
  source "$1"; load_sideyard_runtime_state staging
  [ "$SIDEYARD_BACKEND" = azure_sql ] && [ "$SIDEYARD_GENERATION" = aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa ]
' bash "$ROOT/scripts/azure/runtime-state.sh"

cp "$state" "$WORK/state/good"
for contents in \
  'DATABASE_BACKEND=mysql|DATA_GENERATION=aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa' \
  'DATABASE_BACKEND=azure_sql|DATA_GENERATION=invalid' \
  'DATABASE_BACKEND=azure_sql|DATA_GENERATION=aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa|EXTRA=1'; do
  printf '%s\n' "$contents" | tr '|' '\n' > "$state"
  chmod 600 "$state"
  if SIDEYARD_STATE_ROOT="$WORK/state" SIDEYARD_STATE_TEST_OWNER=1 bash -c 'source "$1"; load_sideyard_runtime_state staging' bash "$ROOT/scripts/azure/runtime-state.sh" >/dev/null 2>&1; then
    echo 'unsafe runtime state was accepted' >&2
    exit 1
  fi
done
mv "$WORK/state/good" "$state"
chmod 600 "$state"

cat > "$WORK/bin/docker" <<'SH'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$SIDEYARD_TEST_DOCKER_LOG"
case "$1 $2" in
  'inspect -f')
    case "$3" in
      *State.Running*) echo true ;;
      *sideyard.backend*) echo azure_sql ;;
      *sideyard.generation*) echo aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa ;;
      *) exit 1 ;;
    esac
    ;;
  'exec clubdue-staging-current')
    if [ -n "${SIDEYARD_TEST_CONCURRENT_WAKE:-}" ] && [ ! -e "${SIDEYARD_TEST_CONCURRENT_WAKE}.done" ]; then
      : > "$SIDEYARD_TEST_CONCURRENT_WAKE"
      : > "${SIDEYARD_TEST_CONCURRENT_WAKE}.done"
    fi
    if [ -n "${SIDEYARD_TEST_DOCKER_EXIT_SEQUENCE:-}" ] && [ -s "$SIDEYARD_TEST_DOCKER_EXIT_SEQUENCE" ]; then
      command_status="$(head -1 "$SIDEYARD_TEST_DOCKER_EXIT_SEQUENCE")"
      sed -i '1d' "$SIDEYARD_TEST_DOCKER_EXIT_SEQUENCE"
      exit "$command_status"
    fi
    ;;
  *) exit 1 ;;
esac
SH
chmod 755 "$WORK/bin/docker"
: > "$WORK/docker.log"
# No wake performs no SQL/container command.
PATH="$WORK/bin:$PATH" SIDEYARD_STATE_ROOT="$WORK/state" SIDEYARD_STATE_TEST_OWNER=1 \
  SIDEYARD_LOCK_ROOT="$WORK/lock" SIDEYARD_WAKE_ROOT="$WORK/wake" \
  SIDEYARD_TEST_DOCKER_LOG="$WORK/docker.log" bash "$ROOT/scripts/azure/run-interest-delivery.sh" staging
[ ! -s "$WORK/docker.log" ]

: > "$WORK/wake/staging/interest.wake"
PATH="$WORK/bin:$PATH" SIDEYARD_STATE_ROOT="$WORK/state" SIDEYARD_STATE_TEST_OWNER=1 \
  SIDEYARD_LOCK_ROOT="$WORK/lock" SIDEYARD_WAKE_ROOT="$WORK/wake" \
  SIDEYARD_TEST_DOCKER_LOG="$WORK/docker.log" bash "$ROOT/scripts/azure/run-interest-delivery.sh" staging
grep -Fx 'exec clubdue-staging-current /clubdue deliver-interest' "$WORK/docker.log"
[ ! -e "$WORK/wake/staging/interest.wake" ]

# A hard kill after the atomic claim leaves a stable claim. The next service
# invocation reconstitutes it before the empty check and drains the queue.
: > "$WORK/wake/staging/interest.wake"
if PATH="$WORK/bin:$PATH" SIDEYARD_STATE_ROOT="$WORK/state" SIDEYARD_STATE_TEST_OWNER=1 \
  SIDEYARD_LOCK_ROOT="$WORK/lock" SIDEYARD_WAKE_ROOT="$WORK/wake" \
  SIDEYARD_TEST_DOCKER_LOG="$WORK/docker.log" SIDEYARD_TEST_HARD_KILL_AFTER_CLAIM=1 \
  bash "$ROOT/scripts/azure/run-interest-delivery.sh" staging >/dev/null 2>&1; then
  echo 'hard-kill claim seam did not stop interest delivery' >&2
  exit 1
fi
[ ! -e "$WORK/wake/staging/interest.wake" ]
[ -f "$WORK/wake/staging/interest.wake.claimed" ]
: > "$WORK/docker.log"
PATH="$WORK/bin:$PATH" SIDEYARD_STATE_ROOT="$WORK/state" SIDEYARD_STATE_TEST_OWNER=1 \
  SIDEYARD_LOCK_ROOT="$WORK/lock" SIDEYARD_WAKE_ROOT="$WORK/wake" \
  SIDEYARD_TEST_DOCKER_LOG="$WORK/docker.log" bash "$ROOT/scripts/azure/run-interest-delivery.sh" staging
grep -Fx 'exec clubdue-staging-current /clubdue deliver-interest' "$WORK/docker.log"
[ ! -e "$WORK/wake/staging/interest.wake" ]
[ ! -e "$WORK/wake/staging/interest.wake.claimed" ]

# A signup committed while a successful drain is finishing creates a new
# canonical wake marker. The wrapper must preserve it and drain once more.
: > "$WORK/wake/staging/interest.wake"
rm -f "$WORK/wake/staging/interest.wake.done"
: > "$WORK/docker.log"
PATH="$WORK/bin:$PATH" SIDEYARD_STATE_ROOT="$WORK/state" SIDEYARD_STATE_TEST_OWNER=1 \
  SIDEYARD_LOCK_ROOT="$WORK/lock" SIDEYARD_WAKE_ROOT="$WORK/wake" \
  SIDEYARD_TEST_DOCKER_LOG="$WORK/docker.log" \
  SIDEYARD_TEST_CONCURRENT_WAKE="$WORK/wake/staging/interest.wake" \
  bash "$ROOT/scripts/azure/run-interest-delivery.sh" staging
[ "$(grep -Fc 'exec clubdue-staging-current /clubdue deliver-interest' "$WORK/docker.log")" = 2 ]
[ ! -e "$WORK/wake/staging/interest.wake" ]

printf '75\n0\n' > "$WORK/exit-sequence"
: > "$WORK/wake/staging/interest.wake"
: > "$WORK/docker.log"
PATH="$WORK/bin:$PATH" SIDEYARD_STATE_ROOT="$WORK/state" SIDEYARD_STATE_TEST_OWNER=1 \
  SIDEYARD_LOCK_ROOT="$WORK/lock" SIDEYARD_WAKE_ROOT="$WORK/wake" \
  SIDEYARD_TEST_DOCKER_LOG="$WORK/docker.log" SIDEYARD_TEST_DOCKER_EXIT_SEQUENCE="$WORK/exit-sequence" \
  bash "$ROOT/scripts/azure/run-interest-delivery.sh" staging
[ "$(grep -Fc 'exec clubdue-staging-current /clubdue deliver-interest' "$WORK/docker.log")" = 2 ]
[ ! -e "$WORK/wake/staging/interest.wake" ]

printf '76\n' > "$WORK/exit-sequence"
: > "$WORK/wake/staging/interest.wake"
if PATH="$WORK/bin:$PATH" SIDEYARD_STATE_ROOT="$WORK/state" SIDEYARD_STATE_TEST_OWNER=1 \
  SIDEYARD_LOCK_ROOT="$WORK/lock" SIDEYARD_WAKE_ROOT="$WORK/wake" \
  SIDEYARD_TEST_DOCKER_LOG="$WORK/docker.log" SIDEYARD_TEST_DOCKER_EXIT_SEQUENCE="$WORK/exit-sequence" \
  bash "$ROOT/scripts/azure/run-interest-delivery.sh" staging >/dev/null 2>&1; then
  echo 'deferred provider delivery did not fail for scheduled retry' >&2
  exit 1
fi
[ -f "$WORK/wake/staging/interest.wake" ]

victim="$WORK/lock-victim"
printf 'unchanged\n' > "$victim"
ln -s "$victim" "$WORK/hostile-lock-root"
if SIDEYARD_LOCK_ROOT="$WORK/hostile-lock-root" SIDEYARD_STATE_TEST_OWNER=1 bash -c 'source "$1"; prepare_sideyard_lock_root' bash "$ROOT/scripts/azure/runtime-state.sh" >/dev/null 2>&1; then
  echo 'symbolic-link lock root was accepted' >&2
  exit 1
fi
[ "$(cat "$victim")" = unchanged ]

cat > "$WORK/backup-runner" <<'SH'
#!/usr/bin/env bash
printf 'backup %s\n' "$1" >> "$SIDEYARD_TEST_BACKUP_LOG"
exit "${SIDEYARD_TEST_BACKUP_EXIT:-0}"
SH
chmod 755 "$WORK/backup-runner"
mkdir -p "$WORK/operations"
: > "$WORK/backup.log"
printf '1\n' > "$WORK/exit-sequence"
if PATH="$WORK/bin:$PATH" SIDEYARD_STATE_ROOT="$WORK/state" SIDEYARD_STATE_TEST_OWNER=1 \
  SIDEYARD_LOCK_ROOT="$WORK/lock" SIDEYARD_OPERATIONS_ROOT="$WORK/operations" \
  SIDEYARD_BACKUP_RUNNER="$WORK/backup-runner" SIDEYARD_TEST_BACKUP_LOG="$WORK/backup.log" \
  SIDEYARD_TEST_DOCKER_LOG="$WORK/docker.log" SIDEYARD_TEST_DOCKER_EXIT_SEQUENCE="$WORK/exit-sequence" \
  bash "$ROOT/scripts/azure/run-database-window.sh" staging >/dev/null 2>&1; then
  echo 'failed scheduled work incorrectly completed its database window' >&2
  exit 1
fi
grep -Fx 'backup staging' "$WORK/backup.log"
[ ! -e "$WORK/operations/staging-database-window.last-success" ]

: > "$WORK/backup.log"
: > "$WORK/exit-sequence"
PATH="$WORK/bin:$PATH" SIDEYARD_STATE_ROOT="$WORK/state" SIDEYARD_STATE_TEST_OWNER=1 \
  SIDEYARD_LOCK_ROOT="$WORK/lock" SIDEYARD_OPERATIONS_ROOT="$WORK/operations" \
  SIDEYARD_BACKUP_RUNNER="$WORK/backup-runner" SIDEYARD_TEST_BACKUP_LOG="$WORK/backup.log" \
  SIDEYARD_TEST_DOCKER_LOG="$WORK/docker.log" SIDEYARD_TEST_DOCKER_EXIT_SEQUENCE="$WORK/exit-sequence" \
  bash "$ROOT/scripts/azure/run-database-window.sh" staging >/dev/null
grep -Fx 'backup staging' "$WORK/backup.log"
grep -qx 'backend=azure_sql' "$WORK/operations/staging-database-window.last-success"
grep -qx "generation=$generation" "$WORK/operations/staging-database-window.last-success"
if PATH="$WORK/bin:$PATH" SIDEYARD_STATE_ROOT="$WORK/state" SIDEYARD_STATE_TEST_OWNER=1 \
  SIDEYARD_LOCK_ROOT="$WORK/lock" bash "$ROOT/scripts/azure/run-interest-delivery.sh" production >/dev/null 2>&1; then
  echo 'production host delivery was accepted' >&2
  exit 1
fi

# A retained PostgreSQL release cannot even be started when Azure and a new
# data generation are authoritative. Exercise the real deploy script up to its
# fail-closed interlock with disposable command and filesystem seams.
cat > "$WORK/bin/docker" <<'PY'
#!/usr/bin/env python3
import json, os, pathlib, sys

args = sys.argv[1:]
log = pathlib.Path(os.environ["SIDEYARD_TEST_DOCKER_LOG"])
with log.open("a") as output:
    output.write(" ".join(args) + "\n")
state_path = pathlib.Path(os.environ["SIDEYARD_TEST_DOCKER_STATE"])
if state_path.exists():
    state = json.loads(state_path.read_text())
else:
    generation = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
    state = {
        "clubdue-staging-current": {"id": "current-id", "running": True, "restart": "unless-stopped", "backend": "azure_sql", "generation": generation, "port": "8082"},
        "clubdue-staging-previous": {"id": "previous-id", "running": False, "restart": "no", "backend": os.environ.get("SIDEYARD_TEST_PREVIOUS_BACKEND", "postgres"), "generation": os.environ.get("SIDEYARD_TEST_PREVIOUS_GENERATION", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"), "port": "18082"},
    }

def save():
    state_path.write_text(json.dumps(state))

def resolve(value):
    if value in state:
        return value, state[value]
    for name, item in state.items():
        if item["id"] == value:
            return name, item
    raise KeyError(value)

failure = os.environ.get("SIDEYARD_TEST_FAIL_ONCE", "")
failure_marker = pathlib.Path(os.environ.get("SIDEYARD_TEST_FAILURE_MARKER", str(state_path) + ".failure"))
if failure and " ".join(args).startswith(failure):
    occurrence = int(os.environ.get("SIDEYARD_TEST_FAIL_OCCURRENCE", "1"))
    seen = int(failure_marker.read_text()) if failure_marker.exists() else 0
    seen += 1
    failure_marker.write_text(str(seen))
    if seen == occurrence:
        save()
        raise SystemExit(1)

try:
    if args[:2] == ["container", "inspect"]:
        resolve(args[-1])
    elif args[0] == "port":
        _, item = resolve(args[1])
        if item["running"]: print("127.0.0.1:" + item["port"])
    elif args[:2] == ["inspect", "-f"]:
        name, item = resolve(args[-1]); template = args[2]
        if ".State.Running" in template: print(str(item["running"]).lower())
        elif ".HostConfig.PortBindings" in template:
            print(os.environ.get("SIDEYARD_TEST_STOPPED_BINDING", "127.0.0.1:" + item["port"]))
        elif "sideyard.backend" in template: print(item["backend"])
        elif "sideyard.generation" in template: print(item["generation"])
        elif ".Id" in template: print(item["id"])
        elif "RestartPolicy.Name" in template: print(item["restart"])
        elif ".Name" in template: print("/" + name)
        else: raise KeyError(template)
    elif args[0] == "start":
        _, item = resolve(args[1]); item["running"] = True
    elif args[0] == "stop":
        _, item = resolve(args[-1]); item["running"] = False
    elif args[0] == "update":
        _, item = resolve(args[-1]); item["restart"] = args[1].split("=", 1)[1]
    elif args[0] == "rename":
        old, item = resolve(args[1]); new = args[2]
        if new in state: raise KeyError(new)
        del state[old]; state[new] = item
    elif args[0] == "rm":
        old, _ = resolve(args[-1]); del state[old]
    elif args[0] in ("login", "pull") or args[:2] == ["network", "create"]:
        pass
    elif args[0] == "run":
        name = args[args.index("--name") + 1]
        mapping = args[args.index("-p") + 1]
        state[name] = {"id": "candidate-id", "running": True, "restart": "no", "backend": "azure_sql", "generation": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "port": mapping.split(":")[-2]}
    else:
        raise KeyError("unsupported")
except KeyError:
    save()
    raise SystemExit(1)
save()
PY
cat > "$WORK/bin/curl" <<'SH'
#!/usr/bin/env bash
if [[ "${@: -1}" == */readyz ]]; then
  echo ok
  exit 0
fi
cat <<'EOF'
demo-sandbox-v2
environment=staging
backend=azure_sql
generation=aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa
EOF
SH
cat > "$WORK/bin/nginx" <<'SH'
#!/usr/bin/env bash
exit 0
SH
cat > "$WORK/bin/systemctl" <<'SH'
#!/usr/bin/env bash
exit 0
SH
chmod 755 "$WORK/bin/docker" "$WORK/bin/curl" "$WORK/bin/nginx" "$WORK/bin/systemctl"
mkdir -p "$WORK/site"
cat > "$WORK/site/staging.conf" <<'EOF'
server {
  server_name staging.test;
  proxy_pass http://127.0.0.1:8082;
}
EOF
ln -s "$WORK/site/staging.conf" "$WORK/site/enabled.conf"
: > "$WORK/docker.log"
rm -f "$WORK/docker-state.json" "$WORK/docker-state.json.failure"
if PATH="$WORK/bin:$PATH" CONTAINER=clubdue-staging PORT=8082 DOMAIN=staging.test CLUBDUE_DEPLOY_MODE=rollback \
  SIDEYARD_STATE_ROOT="$WORK/state" SIDEYARD_STATE_TEST_OWNER=1 SIDEYARD_LOCK_ROOT="$WORK/lock" \
  SIDEYARD_SITE_PATH="$WORK/site/staging.conf" SIDEYARD_ENABLED_PATH="$WORK/site/enabled.conf" \
  SIDEYARD_TEST_DOCKER_LOG="$WORK/docker.log" SIDEYARD_TEST_DOCKER_STATE="$WORK/docker-state.json" \
  bash "$ROOT/scripts/droplet-deploy.sh" >/dev/null 2>&1; then
  echo 'cross-backend rollback was accepted' >&2
  exit 1
fi
if grep -Eq '^(start|rename|update|stop) ' "$WORK/docker.log"; then
  echo 'cross-backend rollback mutated a retained container' >&2
  exit 1
fi

# A stopped release exposes only configured bindings. Reject missing, public,
# reserved, cross-environment and ambiguous bindings before starting it.
for binding in '' '0.0.0.0:18082' '127.0.0.1:8080' '127.0.0.1:8081' '127.0.0.1:28083' $'127.0.0.1:18082\n127.0.0.1:8082'; do
  : > "$WORK/docker.log"
  rm -f "$WORK/docker-state.json" "$WORK/docker-state.json.failure"
  if SIDEYARD_TEST_STOPPED_BINDING="$binding" \
    SIDEYARD_TEST_PREVIOUS_BACKEND=azure_sql SIDEYARD_TEST_PREVIOUS_GENERATION="$generation" \
    PATH="$WORK/bin:$PATH" CONTAINER=clubdue-staging PORT=8082 DOMAIN=staging.test CLUBDUE_DEPLOY_MODE=rollback \
    SIDEYARD_STATE_ROOT="$WORK/state" SIDEYARD_STATE_TEST_OWNER=1 SIDEYARD_LOCK_ROOT="$WORK/lock" \
    SIDEYARD_SITE_PATH="$WORK/site/staging.conf" SIDEYARD_ENABLED_PATH="$WORK/site/enabled.conf" \
    SIDEYARD_TEST_DOCKER_LOG="$WORK/docker.log" SIDEYARD_TEST_DOCKER_STATE="$WORK/docker-state.json" \
    bash "$ROOT/scripts/droplet-deploy.sh" >/dev/null 2>&1; then
    echo "unsafe retained release binding was accepted: $binding" >&2
    exit 1
  fi
  if grep -Eq '^(start|rename|update|stop) ' "$WORK/docker.log"; then
    echo 'unsafe retained release binding mutated a container' >&2
    exit 1
  fi
done

: > "$WORK/docker.log"
rm -f "$WORK/docker-state.json" "$WORK/docker-state.json.failure"
SIDEYARD_TEST_PREVIOUS_BACKEND=azure_sql SIDEYARD_TEST_PREVIOUS_GENERATION="$generation" \
  PATH="$WORK/bin:$PATH" CONTAINER=clubdue-staging PORT=8082 DOMAIN=staging.test CLUBDUE_DEPLOY_MODE=rollback \
  SIDEYARD_STATE_ROOT="$WORK/state" SIDEYARD_STATE_TEST_OWNER=1 SIDEYARD_LOCK_ROOT="$WORK/lock" \
  SIDEYARD_SITE_PATH="$WORK/site/staging.conf" SIDEYARD_ENABLED_PATH="$WORK/site/enabled.conf" \
  SIDEYARD_TEST_DOCKER_LOG="$WORK/docker.log" SIDEYARD_TEST_DOCKER_STATE="$WORK/docker-state.json" \
  bash "$ROOT/scripts/droplet-deploy.sh" >/dev/null
grep -Fx 'update --restart=no clubdue-staging-previous' "$WORK/docker.log"
grep -Fx 'start clubdue-staging-previous' "$WORK/docker.log"
grep -Fx 'stop --timeout 110 current-id' "$WORK/docker.log"
[ "$(SIDEYARD_TEST_DOCKER_LOG="$WORK/docker.log" SIDEYARD_TEST_DOCKER_STATE="$WORK/docker-state.json" "$WORK/bin/docker" inspect -f '{{.Id}}' clubdue-staging-current)" = previous-id ]
[ "$(SIDEYARD_TEST_DOCKER_LOG="$WORK/docker.log" SIDEYARD_TEST_DOCKER_STATE="$WORK/docker-state.json" "$WORK/bin/docker" inspect -f '{{.State.Running}}' clubdue-staging-current)" = true ]
[ "$(SIDEYARD_TEST_DOCKER_LOG="$WORK/docker.log" SIDEYARD_TEST_DOCKER_STATE="$WORK/docker-state.json" "$WORK/bin/docker" inspect -f '{{.HostConfig.RestartPolicy.Name}}' clubdue-staging-current)" = unless-stopped ]
[ "$(SIDEYARD_TEST_DOCKER_LOG="$WORK/docker.log" SIDEYARD_TEST_DOCKER_STATE="$WORK/docker-state.json" "$WORK/bin/docker" inspect -f '{{.HostConfig.RestartPolicy.Name}}' clubdue-staging-previous)" = no ]

assert_recovered_release() {
  local expected_id="$1" expected_port="$2"
  [ "$(SIDEYARD_TEST_DOCKER_LOG="$WORK/docker.log" SIDEYARD_TEST_DOCKER_STATE="$WORK/docker-state.json" "$WORK/bin/docker" inspect -f '{{.Id}}' clubdue-staging-current)" = "$expected_id" ]
  [ "$(SIDEYARD_TEST_DOCKER_LOG="$WORK/docker.log" SIDEYARD_TEST_DOCKER_STATE="$WORK/docker-state.json" "$WORK/bin/docker" inspect -f '{{.State.Running}}' clubdue-staging-current)" = true ]
  [ "$(SIDEYARD_TEST_DOCKER_LOG="$WORK/docker.log" SIDEYARD_TEST_DOCKER_STATE="$WORK/docker-state.json" "$WORK/bin/docker" inspect -f '{{.HostConfig.RestartPolicy.Name}}' clubdue-staging-current)" = unless-stopped ]
  [ "$(SIDEYARD_TEST_DOCKER_LOG="$WORK/docker.log" SIDEYARD_TEST_DOCKER_STATE="$WORK/docker-state.json" "$WORK/bin/docker" inspect -f '{{.State.Running}}' clubdue-staging-previous)" = false ]
  [ "$(SIDEYARD_TEST_DOCKER_LOG="$WORK/docker.log" SIDEYARD_TEST_DOCKER_STATE="$WORK/docker-state.json" "$WORK/bin/docker" inspect -f '{{.HostConfig.RestartPolicy.Name}}' clubdue-staging-previous)" = no ]
  grep -q "proxy_pass http://127.0.0.1:${expected_port};" "$WORK/site/staging.conf"
}

for scenario in \
  'update --restart=no current-id|1' \
  'update --restart=no current-id|2' \
  'update --restart=unless-stopped previous-id|2' \
  'stop --timeout 110 current-id|1' \
  'rename current-id clubdue-staging-finalizing|1' \
  'rename previous-id clubdue-staging-current|1' \
  'rename current-id clubdue-staging-previous|1'; do
  failure="${scenario%|*}"
  failure_occurrence="${scenario##*|}"
  rm -f "$WORK/docker-state.json" "$WORK/docker-state.json.failure"
  sed -E -i 's#proxy_pass http://127\.0\.0\.1:(8082|18082);#proxy_pass http://127.0.0.1:8082;#' "$WORK/site/staging.conf"
  if SIDEYARD_TEST_PREVIOUS_BACKEND=azure_sql SIDEYARD_TEST_PREVIOUS_GENERATION="$generation" \
    SIDEYARD_TEST_FAIL_ONCE="$failure" SIDEYARD_TEST_FAIL_OCCURRENCE="$failure_occurrence" PATH="$WORK/bin:$PATH" CONTAINER=clubdue-staging PORT=8082 DOMAIN=staging.test CLUBDUE_DEPLOY_MODE=rollback \
    SIDEYARD_STATE_ROOT="$WORK/state" SIDEYARD_STATE_TEST_OWNER=1 SIDEYARD_LOCK_ROOT="$WORK/lock" \
    SIDEYARD_SITE_PATH="$WORK/site/staging.conf" SIDEYARD_ENABLED_PATH="$WORK/site/enabled.conf" \
    SIDEYARD_TEST_DOCKER_LOG="$WORK/docker.log" SIDEYARD_TEST_DOCKER_STATE="$WORK/docker-state.json" \
    bash "$ROOT/scripts/droplet-deploy.sh" >/dev/null 2>&1; then
    echo "rollback fault was not surfaced: $failure" >&2
    exit 1
  fi
  assert_recovered_release previous-id 18082
done

cat > "$WORK/staging-db.env" <<'EOF'
POSTGRES_PASSWORD=unused-test-password
SESSION_SECRET=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
EOF
cat > "$WORK/staging-sql-runtime.env" <<'EOF'
DATABASE_BACKEND=azure_sql
SCHEDULED_WORK_OWNER=host
INTEREST_WAKE_FILE=/var/run/sideyard/interest.wake
EOF
chmod 600 "$WORK/staging-db.env" "$WORK/staging-sql-runtime.env"
for scenario in \
  'update --restart=no current-id|1' \
  'update --restart=no current-id|2' \
  'update --restart=unless-stopped candidate-id|2' \
  'stop --timeout 110 current-id|1' \
  'rename current-id clubdue-staging-finalizing|1' \
  'rename candidate-id clubdue-staging-current|1' \
  'rename current-id clubdue-staging-previous|1'; do
  failure="${scenario%|*}"
  failure_occurrence="${scenario##*|}"
  rm -f "$WORK/docker-state.json" "$WORK/docker-state.json.failure"
  sed -E -i 's#proxy_pass http://127\.0\.0\.1:(8082|18082);#proxy_pass http://127.0.0.1:8082;#' "$WORK/site/staging.conf"
  if SIDEYARD_TEST_PREVIOUS_BACKEND=azure_sql SIDEYARD_TEST_PREVIOUS_GENERATION="$generation" \
    SIDEYARD_TEST_FAIL_ONCE="$failure" SIDEYARD_TEST_FAIL_OCCURRENCE="$failure_occurrence" PATH="$WORK/bin:$PATH" CONTAINER=clubdue-staging PORT=8082 DOMAIN=staging.test CLUBDUE_DEPLOY_MODE=promote \
    IMAGE=example.test/sideyard:reviewed GHCR_USER=test GHCR_TOKEN=test \
    SIDEYARD_ENV_FILE="$WORK/staging-db.env" SIDEYARD_SQL_ENV_FILE="$WORK/staging-sql-runtime.env" \
    SIDEYARD_DEPLOY_TEST_SKIP_CANDIDATE_CONTRACT=1 \
    SIDEYARD_STATE_ROOT="$WORK/state" SIDEYARD_STATE_TEST_OWNER=1 SIDEYARD_LOCK_ROOT="$WORK/lock" SIDEYARD_WAKE_ROOT="$WORK/wake" \
    SIDEYARD_SITE_PATH="$WORK/site/staging.conf" SIDEYARD_ENABLED_PATH="$WORK/site/enabled.conf" \
    SIDEYARD_TEST_DOCKER_LOG="$WORK/docker.log" SIDEYARD_TEST_DOCKER_STATE="$WORK/docker-state.json" \
    bash "$ROOT/scripts/droplet-deploy.sh" >/dev/null 2>&1; then
    echo "promotion fault was not surfaced: $failure" >&2
    exit 1
  fi
  assert_recovered_release candidate-id 18082
done

# A process or host death cannot run an EXIT trap. Persisted finalization state
# must make each container rename boundary restartable on the next invocation.
for phase in retained-to-finalizing target-to-current retained-to-previous; do
  rm -f "$WORK/docker-state.json" "$WORK/docker-state.json.failure" \
    "$WORK/state/staging-release-finalization.env"
  sed -E -i 's#proxy_pass http://127\.0\.0\.1:(8082|18082);#proxy_pass http://127.0.0.1:8082;#' "$WORK/site/staging.conf"
  if SIDEYARD_TEST_PREVIOUS_BACKEND=azure_sql SIDEYARD_TEST_PREVIOUS_GENERATION="$generation" \
    SIDEYARD_TEST_HARD_KILL_AFTER="$phase" PATH="$WORK/bin:$PATH" CONTAINER=clubdue-staging PORT=8082 DOMAIN=staging.test CLUBDUE_DEPLOY_MODE=promote \
    IMAGE=example.test/sideyard:reviewed GHCR_USER=test GHCR_TOKEN=test \
    SIDEYARD_ENV_FILE="$WORK/staging-db.env" SIDEYARD_SQL_ENV_FILE="$WORK/staging-sql-runtime.env" \
    SIDEYARD_DEPLOY_TEST_SKIP_CANDIDATE_CONTRACT=1 \
    SIDEYARD_STATE_ROOT="$WORK/state" SIDEYARD_STATE_TEST_OWNER=1 SIDEYARD_LOCK_ROOT="$WORK/lock" SIDEYARD_WAKE_ROOT="$WORK/wake" \
    SIDEYARD_SITE_PATH="$WORK/site/staging.conf" SIDEYARD_ENABLED_PATH="$WORK/site/enabled.conf" \
    SIDEYARD_TEST_DOCKER_LOG="$WORK/docker.log" SIDEYARD_TEST_DOCKER_STATE="$WORK/docker-state.json" \
    bash "$ROOT/scripts/droplet-deploy.sh" >/dev/null 2>&1; then
    echo "hard-kill finalization seam did not stop deployment: $phase" >&2
    exit 1
  fi
  [ -f "$WORK/state/staging-release-finalization.env" ]
  PATH="$WORK/bin:$PATH" CONTAINER=clubdue-staging PORT=8082 DOMAIN=staging.test CLUBDUE_DEPLOY_MODE=promote \
    IMAGE=example.test/sideyard:reviewed GHCR_USER=test GHCR_TOKEN=test \
    SIDEYARD_DEPLOY_TEST_RECOVERY_ONLY=1 SIDEYARD_STATE_ROOT="$WORK/state" SIDEYARD_STATE_TEST_OWNER=1 \
    SIDEYARD_LOCK_ROOT="$WORK/lock" SIDEYARD_SITE_PATH="$WORK/site/staging.conf" SIDEYARD_ENABLED_PATH="$WORK/site/enabled.conf" \
    SIDEYARD_TEST_DOCKER_LOG="$WORK/docker.log" SIDEYARD_TEST_DOCKER_STATE="$WORK/docker-state.json" \
    bash "$ROOT/scripts/droplet-deploy.sh" >/dev/null
  [ ! -e "$WORK/state/staging-release-finalization.env" ]
  assert_recovered_release candidate-id 18082
done

echo 'Azure runtime operation contracts passed'
