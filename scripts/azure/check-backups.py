#!/usr/bin/env python3
"""Alert the Sideyard owner for failed or stale encrypted Azure exports."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import time
import urllib.request
import urllib.error


def backup_problem(now, last_success, failed):
    if failed:
        return "The last scheduled backup failed."
    if last_success is None or last_success > now + 300 or now - last_success > 26 * 3600:
        return "No verified Azure backup has completed in the last 26 hours."
    return ""


def send_notice(env, environment, incident, message, opener):
    if env.get("INTEREST_NOTIFY_TO") != "wmichelin@gmail.com":
        raise ValueError("backup notifications require the configured Sideyard owner address")
    data = json.dumps({"from": env["INTEREST_NOTIFY_FROM"], "to": [env["INTEREST_NOTIFY_TO"]], "subject": f"Sideyard {environment} backup alert", "text": message + "\n\nInspect sideyard-azure-backup@" + environment + ".service and the Azure backup manifest before relying on recovery."}).encode()
    request = urllib.request.Request("https://api.resend.com/emails", data=data, headers={"Authorization": "Bearer " + env["RESEND_API_KEY"], "Content-Type": "application/json", "Idempotency-Key": f"sideyard-backups/{environment}/{incident}"})
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
    try:
        last_success = int((root / f"{environment}.last-success").read_text().splitlines()[0])
    except (OSError, ValueError, IndexError):
        last_success = None
    failed = subprocess.run(["systemctl", "is-failed", "--quiet", f"sideyard-azure-backup@{environment}.service"], check=False).returncode == 0
    problem = backup_problem(now, last_success, failed)
    state = root / f"{environment}.alert-state"
    if not problem:
        state.unlink(missing_ok=True)
        return
    if state.exists():
        incident = json.loads(state.read_text())
    else:
        incident = {"id": str(now), "sent": False}
        state.write_text(json.dumps(incident))
    if incident["sent"]:
        return
    # Configuration is passed by systemd, never sourced as shell code.
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *args, **kwargs):
            return None
    send_notice(os.environ, environment, incident["id"], problem, urllib.request.build_opener(NoRedirect()))
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
