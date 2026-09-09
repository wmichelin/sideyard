#!/usr/bin/env python3
"""Create an environment-scoped Entra identity; save its secret outside Git.

Requires signed-in Azure CLI on PATH. This creates no role assignment: put the
printed principal object ID in the environment's Terraform variables and apply.
Existing credentials are preserved; rotation is an explicit separate operation.
"""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess


def az(*args):
    result = subprocess.run(["az", *args, "--output", "json"], capture_output=True, text=True)
    if result.returncode:
        raise SystemExit("Azure identity operation failed; verify the signed-in tenant and directory permissions")
    return json.loads(result.stdout)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("environment", choices=["staging", "production"])
    parser.add_argument("purpose", choices=["photos", "backups"])
    parser.add_argument("account")
    parser.add_argument("--expires", required=True, help="ISO timestamp for deliberate credential rotation")
    args = parser.parse_args()
    if not re.fullmatch(r"sideyard[a-z0-9]{1,16}", args.account):
        parser.error("invalid Sideyard storage account")
    account = az("account", "show")
    name = f"sideyard-{args.environment}-{args.purpose}"
    apps = [app for app in az("ad", "app", "list", "--display-name", name) if app["displayName"] == name]
    if len(apps) > 1:
        raise SystemExit("Ambiguous app identity; inspect duplicate names")
    app = apps[0] if apps else az("ad", "app", "create", "--display-name", name, "--sign-in-audience", "AzureADMyOrg")
    app_id = app["appId"]
    principals = az("ad", "sp", "list", "--filter", f"appId eq '{app_id}'")
    principal = principals[0] if principals else az("ad", "sp", "create", "--id", app_id)
    folder = Path.home() / ".config" / "sideyard-azure"
    folder.mkdir(parents=True, exist_ok=True)
    folder.chmod(0o700)
    target = folder / f"{args.environment}-{args.purpose}.env"
    if not target.exists():
        credential = az("ad", "app", "credential", "reset", "--id", app_id, "--append", "--display-name", name, "--end-date", args.expires)
        values = {"AZURE_TENANT_ID": account["tenantId"], "AZURE_CLIENT_ID": app_id, "AZURE_CLIENT_SECRET": credential["password"]}
        if args.purpose == "photos":
            values["AZURE_BLOB_ACCOUNT_URL"] = f"https://{args.account}.blob.core.windows.net"
        else:
            values["AZURE_BACKUP_ACCOUNT"] = args.account
        descriptor = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(descriptor, "w") as output:
            output.write("".join(f"{key}={value}\n" for key, value in values.items()))
    print(json.dumps({"identity": name, "client_id": app_id, "principal_object_id": principal["id"], "credential_file": str(target), "role_assignment": "requires reviewed Terraform apply"}))


if __name__ == "__main__":
    main()
