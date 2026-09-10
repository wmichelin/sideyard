#!/usr/bin/env python3
"""Alert the Sideyard owner for failed or stale encrypted Azure exports."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import time
import urllib.request
import urllib.error


GENERATION = re.compile(r"[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}")


def read_runtime_state(path, owner_uid=None):
    if path.is_symlink() or not path.is_file() or path.stat().st_mode & 0o777 != 0o600:
        raise ValueError("unsafe backup runtime state")
    if owner_uid is not None and path.stat().st_uid != owner_uid:
        raise ValueError("unsafe backup runtime state")
    lines = path.read_text().splitlines()
    if len(lines) != 2 or not lines[0].startswith("DATABASE_BACKEND=") or not lines[1].startswith("DATA_GENERATION="):
        raise ValueError("invalid backup runtime state")
    backend, generation = lines[0].split("=", 1)[1], lines[1].split("=", 1)[1]
    if backend not in ("postgres", "azure_sql") or not GENERATION.fullmatch(generation):
        raise ValueError("invalid backup runtime state")
    return backend, generation


def read_success(path, backend, generation):
    try:
        lines = path.read_text().splitlines()
        if len(lines) != 4 or lines[1] != "backend=" + backend or lines[2] != "generation=" + generation or not lines[3].startswith("manifest=") or not lines[3][9:]:
            return None
        return int(lines[0])
    except (OSError, ValueError, IndexError):
        return None


def backup_problem(now, last_success, failed):
    if failed:
        return "The last scheduled backup failed."
    if last_success is None or last_success > now + 300 or now - last_success > 26 * 3600:
        return "No verified Azure backup has completed in the last 26 hours."
    return ""


def send_notice(env, environment, incident, message, service, opener):
    if env.get("INTEREST_NOTIFY_TO") != "wmichelin@gmail.com":
        raise ValueError("backup notifications require the configured Sideyard owner address")
    data = json.dumps({"from": env["INTEREST_NOTIFY_FROM"], "to": [env["INTEREST_NOTIFY_TO"]], "subject": f"Sideyard {environment} backup alert", "text": message + "\n\nInspect " + service + " and its backend/generation-matched Azure backup manifest before relying on recovery."}).encode()
    request = urllib.request.Request("https://api.resend.com/emails", data=data, headers={"Authorization": "Bearer " + env["RESEND_API_KEY"], "Content-Type": "application/json", "User-Agent": "Sideyard/1.0", "Idempotency-Key": f"sideyard-backups/{environment}/{incident}"})
    with opener.open(request, timeout=20) as response:
        result = json.loads(response.read(4096))
        if not result.get("id"):
            raise ValueError("email service did not confirm the backup alert")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("environment", choices=["staging", "production"])
    args = parser.parse_args()
    environment = args.environment
    os.umask(0o077)
    now = int(time.time())
    root = Path("/var/lib/sideyard-backups")
    root.mkdir(parents=True, exist_ok=True)
    state_root = Path(os.environ.get("SIDEYARD_STATE_ROOT", "/var/lib/sideyard-operations"))
    backend, generation = read_runtime_state(state_root / f"{environment}-backend.env", owner_uid=0)
    last_success = read_success(root / f"{environment}.last-success", backend, generation)
    unit = "sideyard-database-window" if backend == "azure_sql" else "sideyard-azure-backup"
    service = f"{unit}@{environment}.service"
    failed = subprocess.run(["systemctl", "is-failed", "--quiet", service], check=False).returncode == 0
    problem = backup_problem(now, last_success, failed)
    state = root / f"{environment}.alert-state"
    if not problem:
        state.unlink(missing_ok=True)
        return
    incident_key = f"{backend}/{generation}/{problem}"
    try:
        incident = json.loads(state.read_text())
    except (OSError, ValueError, TypeError):
        incident = {}
    if incident.get("key") != incident_key:
        incident = {"id": str(now), "key": incident_key, "sent": False}
        state.write_text(json.dumps(incident))
    if incident["sent"]:
        return
    # Configuration is passed by systemd, never sourced as shell code.
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *args, **kwargs):
            return None
    send_notice(os.environ, environment, incident["id"], problem, service, urllib.request.build_opener(NoRedirect()))
    incident["sent"] = True
    temporary = state.with_suffix(".tmp")
    temporary.write_text(json.dumps(incident))
    temporary.replace(state)
    print(f"Sideyard {environment} backup alert accepted by email service")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, KeyError):
        raise SystemExit("Backup monitor failed; check protected configuration and email service availability")
