#!/usr/bin/env python3
"""Create or rotate Sideyard's staging SQL runtime identity without Azure RBAC."""

import argparse
from datetime import datetime, timedelta, timezone
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys


IDENTITY = "sideyard-staging-sql-runtime"
UUID = re.compile(r"^[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$")


def run(command, *, cwd, environment=None):
    try:
        return subprocess.run(command, cwd=cwd, env=environment, check=True, text=True, capture_output=True)
    except subprocess.CalledProcessError as error:
        if error.stderr:
            print(error.stderr.rstrip(), file=sys.stderr)
        raise SystemExit(f"Azure identity subprocess failed with exit status {error.returncode}")


def json_command(command, *, cwd, environment=None):
    return json.loads(run(command, cwd=cwd, environment=environment).stdout)


def parse_timestamp(value, label):
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        raise SystemExit(f"{label} must be an ISO timestamp") from None
    if parsed.tzinfo is None:
        raise SystemExit(f"{label} must include a timezone")
    return parsed.astimezone(timezone.utc)


def terraform_resources(root, terraform_directory):
    environment = os.environ.copy()
    environment["TF_DATA_DIR"] = str(terraform_directory / ".terraform" / "staging")
    backend = terraform_directory / "staging.hcl"
    if not backend.is_file():
        backend = terraform_directory / "staging.local.hcl"
    if not backend.is_file() or not Path(environment["TF_DATA_DIR"]).is_dir():
        raise SystemExit("Azure Terraform state is not initialized for staging")
    resources = json_command(
        ["terraform", f"-chdir={terraform_directory}", "output", "-json", "resources"],
        cwd=root,
        environment=environment,
    )
    database_id = str(resources.get("database_id", ""))
    subscription = re.match(r"^/subscriptions/([^/]+)/", database_id, re.IGNORECASE)
    server = str(resources.get("sql_server", "")).lower()
    if (
        resources.get("environment") != "staging"
        or subscription is None
        or not database_id.lower().endswith("/databases/sideyard")
        or not server.startswith("sideyard-staging-")
        or not server.endswith(".database.windows.net")
    ):
        raise SystemExit("Terraform output does not identify the expected Sideyard staging database")
    return resources, subscription.group(1), server


def read_credential_file(target):
    details = target.lstat()
    if stat.S_ISLNK(details.st_mode) or not stat.S_ISREG(details.st_mode):
        raise SystemExit("SQL runtime credential path must be a regular file, not a link")
    if details.st_uid != os.getuid() or stat.S_IMODE(details.st_mode) & 0o077:
        raise SystemExit("SQL runtime credential file must be owner-only")
    values = {}
    for line in target.read_text().splitlines():
        if not line or "=" not in line:
            raise SystemExit("SQL runtime credential file has an invalid line")
        key, value = line.split("=", 1)
        if key in values or not re.fullmatch(r"[A-Z][A-Z0-9_]+", key):
            raise SystemExit("SQL runtime credential file has an invalid or duplicate key")
        values[key] = value
    return values


def validate_credential_values(values, *, server, tenant, client_id):
    expected = {
        "DATABASE_BACKEND": "azure_sql",
        "AZURE_SQL_SERVER": server,
        "AZURE_SQL_DATABASE": "sideyard",
        "AZURE_SQL_TENANT_ID": tenant,
        "AZURE_SQL_CLIENT_ID": client_id,
        "AZURE_SQL_RUNTIME_CLIENT_ID": client_id,
        "AZURE_SQL_AUTH_MODE": "client_secret",
        "ENABLE_AZURE_SQL_HTTP": "1",
        "SCHEDULED_WORK_OWNER": "host",
        "INTEREST_WAKE_FILE": "/var/run/sideyard/interest.wake",
    }
    allowed = set(expected) | {
        "AZURE_SQL_CLIENT_SECRET",
        "AZURE_SQL_CREDENTIAL_KEY_ID",
        "AZURE_SQL_CREDENTIAL_EXPIRES",
    }
    if set(values) != allowed or any(values.get(key) != value for key, value in expected.items()):
        raise SystemExit("SQL runtime credential file does not match Terraform and Azure identity")
    if not values["AZURE_SQL_CLIENT_SECRET"] or not UUID.fullmatch(values["AZURE_SQL_CREDENTIAL_KEY_ID"]):
        raise SystemExit("SQL runtime credential file is incomplete")
    expiry = parse_timestamp(values["AZURE_SQL_CREDENTIAL_EXPIRES"], "stored credential expiry")
    if expiry <= datetime.now(timezone.utc) + timedelta(days=30):
        raise SystemExit("stored SQL runtime credential expires within 30 days")


def write_credential_file(target, values):
    target.parent.mkdir(parents=True, exist_ok=True)
    target.parent.chmod(0o700)
    temporary = target.with_name(f".{target.name}.{os.getpid()}.new")
    descriptor = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        with os.fdopen(descriptor, "w") as output:
            output.write("".join(f"{key}={value}\n" for key, value in values.items()))
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, target)
        directory = os.open(target.parent, os.O_RDONLY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        if temporary.exists():
            temporary.unlink()


def credential_by_key(credentials, key_id):
    matches = [item for item in credentials if str(item.get("keyId", "")).lower() == key_id.lower()]
    return matches[0] if len(matches) == 1 else None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("environment", choices=("staging",))
    parser.add_argument("--expires", help="ISO timestamp for a new or rotated credential")
    parser.add_argument("--expected-client-id", help="required to adopt an existing app on a new machine")
    action = parser.add_mutually_exclusive_group()
    action.add_argument("--rotate", action="store_true", help="atomically replace the local file with a new credential")
    action.add_argument("--revoke-key-id", help="revoke an old key after the replacement has passed its canary")
    action.add_argument("--rollback-rotation", action="store_true", help="restore the saved prior credential before revocation")
    parser.add_argument("--terraform-directory", type=Path, help="initialized environments directory; defaults to this checkout")
    args = parser.parse_args()
    if args.expected_client_id and not UUID.fullmatch(args.expected_client_id):
        parser.error("--expected-client-id must be a UUID")
    if args.revoke_key_id and not UUID.fullmatch(args.revoke_key_id):
        parser.error("--revoke-key-id must be a UUID")

    root = Path(__file__).resolve().parents[2]
    terraform_directory = args.terraform_directory.resolve() if args.terraform_directory else root / "infra" / "azure" / "environments"
    resources, subscription, server = terraform_resources(root, terraform_directory)
    account = json_command(["az", "account", "show", "--output", "json"], cwd=root)
    if str(account.get("id", "")).lower() != subscription.lower():
        raise SystemExit("Azure CLI subscription does not match the staging Terraform state")
    tenant = str(account.get("tenantId", ""))
    if not UUID.fullmatch(tenant):
        raise SystemExit("Azure CLI did not report a valid tenant identifier")

    target = Path.home() / ".config" / "sideyard-azure" / "staging-sql-runtime.env"
    previous_target = target.with_name(target.name + ".previous")
    existing = read_credential_file(target) if target.exists() or target.is_symlink() else None
    local_client_id = existing.get("AZURE_SQL_CLIENT_ID") if existing else None
    if args.expected_client_id and local_client_id and args.expected_client_id != local_client_id:
        raise SystemExit("expected client ID does not match the existing credential file")
    authorized_client_id = args.expected_client_id or local_client_id

    apps = json_command(["az", "ad", "app", "list", "--display-name", IDENTITY, "--output", "json"], cwd=root)
    apps = [app for app in apps if app.get("displayName") == IDENTITY]
    if len(apps) > 1:
        raise SystemExit("multiple exact staging SQL runtime applications exist")
    if apps:
        app = apps[0]
        if not authorized_client_id or app.get("appId") != authorized_client_id:
            raise SystemExit("refusing to adopt an existing untracked staging SQL runtime application")
    else:
        if authorized_client_id:
            raise SystemExit("expected staging SQL runtime application does not exist")
        app = json_command(["az", "ad", "app", "create", "--display-name", IDENTITY, "--sign-in-audience", "AzureADMyOrg", "--output", "json"], cwd=root)
    client_id = str(app.get("appId", ""))
    if not UUID.fullmatch(client_id):
        raise SystemExit("Azure application did not return a valid client identifier")
    principals = json_command(["az", "ad", "sp", "list", "--filter", f"appId eq '{client_id}'", "--output", "json"], cwd=root)
    if len(principals) > 1:
        raise SystemExit("multiple service principals exist for the staging SQL runtime application")
    principal = principals[0] if principals else json_command(["az", "ad", "sp", "create", "--id", client_id, "--output", "json"], cwd=root)
    principal_id = str(principal.get("id", ""))
    if not UUID.fullmatch(principal_id):
        raise SystemExit("Azure service principal did not return a valid object identifier")

    credentials = json_command(["az", "ad", "app", "credential", "list", "--id", client_id, "--output", "json"], cwd=root)
    if args.rollback_rotation:
        if not existing or not (previous_target.exists() or previous_target.is_symlink()):
            raise SystemExit("both current and saved prior credentials are required for rotation rollback")
        previous = read_credential_file(previous_target)
        validate_credential_values(existing, server=server, tenant=tenant, client_id=client_id)
        validate_credential_values(previous, server=server, tenant=tenant, client_id=client_id)
        if previous["AZURE_SQL_CREDENTIAL_KEY_ID"] == existing["AZURE_SQL_CREDENTIAL_KEY_ID"]:
            raise SystemExit("saved prior credential is not a distinct rotation target")
        if credential_by_key(credentials, previous["AZURE_SQL_CREDENTIAL_KEY_ID"]) is None:
            raise SystemExit("saved prior credential is no longer active in Azure")
        replaced_key = existing["AZURE_SQL_CREDENTIAL_KEY_ID"]
        write_credential_file(target, previous)
        previous_target.unlink()
        print(json.dumps({"identity": IDENTITY, "restored_key_id": previous["AZURE_SQL_CREDENTIAL_KEY_ID"], "revoke_after_canary_key_id": replaced_key}))
        return
    if args.revoke_key_id:
        if not existing:
            raise SystemExit("a current local credential is required before revoking an old key")
        validate_credential_values(existing, server=server, tenant=tenant, client_id=client_id)
        if args.revoke_key_id == existing["AZURE_SQL_CREDENTIAL_KEY_ID"]:
            raise SystemExit("refusing to revoke the key used by the current local credential")
        if credential_by_key(credentials, args.revoke_key_id) is None:
            raise SystemExit("old credential key was not found exactly once")
        run(["az", "ad", "app", "credential", "delete", "--id", client_id, "--key-id", args.revoke_key_id], cwd=root)
        if previous_target.exists() or previous_target.is_symlink():
            previous = read_credential_file(previous_target)
            if previous.get("AZURE_SQL_CREDENTIAL_KEY_ID") == args.revoke_key_id:
                previous_target.unlink()
        print(json.dumps({"identity": IDENTITY, "revoked_key_id": args.revoke_key_id}))
        return

    if existing and not args.rotate:
        validate_credential_values(existing, server=server, tenant=tenant, client_id=client_id)
        live = credential_by_key(credentials, existing["AZURE_SQL_CREDENTIAL_KEY_ID"])
        if live is None:
            raise SystemExit("stored SQL runtime credential key is absent from Azure")
        live_expiry = parse_timestamp(str(live.get("endDateTime", "")), "Azure credential expiry")
        stored_expiry = parse_timestamp(existing["AZURE_SQL_CREDENTIAL_EXPIRES"], "stored credential expiry")
        if abs((live_expiry - stored_expiry).total_seconds()) > 1:
            raise SystemExit("stored SQL runtime credential expiry does not match Azure")
    else:
        if not args.expires:
            raise SystemExit("--expires is required when creating or rotating a credential")
        expiry = parse_timestamp(args.expires, "--expires")
        if expiry <= datetime.now(timezone.utc) + timedelta(days=30):
            raise SystemExit("new SQL runtime credential must remain valid for more than 30 days")
        if existing and not args.expected_client_id:
            raise SystemExit("rotation requires --expected-client-id as an explicit identity check")
        if existing:
            validate_credential_values(existing, server=server, tenant=tenant, client_id=client_id)
            if previous_target.exists() or previous_target.is_symlink():
                raise SystemExit("complete or roll back the prior credential rotation first")
        credential = json_command([
            "az", "ad", "app", "credential", "reset", "--id", client_id,
            "--append", "--display-name", IDENTITY, "--end-date", args.expires, "--output", "json",
        ], cwd=root)
        secret = str(credential.get("password", ""))
        if not secret:
            raise SystemExit("Azure did not return the new runtime credential")
        refreshed = json_command(["az", "ad", "app", "credential", "list", "--id", client_id, "--output", "json"], cwd=root)
        candidates = [item for item in refreshed if item.get("displayName") == IDENTITY and parse_timestamp(str(item.get("endDateTime", "")), "Azure credential expiry") == expiry]
        if not candidates:
            raise SystemExit("new Azure credential metadata could not be identified")
        current = max(candidates, key=lambda item: str(item.get("startDateTime", "")))
        key_id = str(current.get("keyId", ""))
        if not UUID.fullmatch(key_id):
            raise SystemExit("new Azure credential did not return a valid key identifier")
        if existing:
            write_credential_file(previous_target, existing)
        write_credential_file(target, {
            "DATABASE_BACKEND": "azure_sql",
            "AZURE_SQL_SERVER": server,
            "AZURE_SQL_DATABASE": "sideyard",
            "AZURE_SQL_TENANT_ID": tenant,
            "AZURE_SQL_CLIENT_ID": client_id,
            "AZURE_SQL_RUNTIME_CLIENT_ID": client_id,
            "AZURE_SQL_CLIENT_SECRET": secret,
            "AZURE_SQL_AUTH_MODE": "client_secret",
            "ENABLE_AZURE_SQL_HTTP": "1",
            "SCHEDULED_WORK_OWNER": "host",
            "INTEREST_WAKE_FILE": "/var/run/sideyard/interest.wake",
            "AZURE_SQL_CREDENTIAL_KEY_ID": key_id,
            "AZURE_SQL_CREDENTIAL_EXPIRES": current["endDateTime"],
        })
        existing = read_credential_file(target)
        validate_credential_values(existing, server=server, tenant=tenant, client_id=client_id)
        credentials = refreshed

    current_key = existing["AZURE_SQL_CREDENTIAL_KEY_ID"]
    stale_keys = [str(item.get("keyId")) for item in credentials if item.get("keyId") and item.get("keyId") != current_key]
    print(json.dumps({
        "identity": IDENTITY,
        "client_id": client_id,
        "principal_object_id": principal_id,
        "credential_file": str(target),
        "credential_key_id": current_key,
        "credential_expires": existing["AZURE_SQL_CREDENTIAL_EXPIRES"],
        "stale_key_ids": stale_keys,
        "database": resources.get("database_id"),
        "azure_role_assignments": "none; database grants are applied separately",
    }))


if __name__ == "__main__":
    main()
