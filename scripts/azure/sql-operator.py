#!/usr/bin/env python3
"""Run a staging-only Azure SQL operator command against Terraform's exact target."""

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile


def run(command, *, cwd, environment=None, capture=False):
    try:
        return subprocess.run(
            command,
            cwd=cwd,
            env=environment,
            check=True,
            text=True,
            capture_output=capture,
        )
    except subprocess.CalledProcessError as error:
        if capture and error.stderr:
            print(error.stderr.rstrip(), file=sys.stderr)
        raise SystemExit(f"operator subprocess failed with exit status {error.returncode}")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("environment", choices=("staging",))
    parser.add_argument("command", choices=("migrate", "check-db", "maintenance", "bootstrap-runtime"))
    parser.add_argument(
        "--runtime-client-id",
        help="Entra application/client ID; required only for bootstrap-runtime",
    )
    parser.add_argument(
        "--terraform-directory",
        type=Path,
        help="initialized environments directory; defaults to this checkout",
    )
    args = parser.parse_args()
    if args.command == "bootstrap-runtime":
        if not args.runtime_client_id or not re.fullmatch(
            r"[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}",
            args.runtime_client_id,
        ):
            parser.error("bootstrap-runtime requires a UUID --runtime-client-id")
    elif args.runtime_client_id:
        parser.error("--runtime-client-id is valid only with bootstrap-runtime")

    root = Path(__file__).resolve().parents[2]
    terraform_directory = (
        args.terraform_directory.resolve()
        if args.terraform_directory
        else root / "infra" / "azure" / "environments"
    )
    backend = terraform_directory / f"{args.environment}.hcl"
    if not backend.is_file():
        backend = terraform_directory / f"{args.environment}.local.hcl"
    terraform_data = terraform_directory / ".terraform" / args.environment
    if not backend.is_file() or not terraform_data.is_dir():
        raise SystemExit(
            "Azure Terraform state is not initialized for staging; follow infra/azure/README.md"
        )

    operator_environment = os.environ.copy()
    operator_environment["TF_DATA_DIR"] = str(terraform_data)
    output = run(
        ["terraform", f"-chdir={terraform_directory}", "output", "-json", "resources"],
        cwd=root,
        environment=operator_environment,
        capture=True,
    )
    resources = json.loads(output.stdout)
    server = str(resources.get("sql_server", "")).lower()
    database_id = str(resources.get("database_id", ""))
    subscription_match = re.match(r"^/subscriptions/([^/]+)/", database_id, re.IGNORECASE)
    expected_suffix = "/databases/sideyard"
    if (
        resources.get("environment") != "staging"
        or not server.startswith("sideyard-staging-")
        or not server.endswith(".database.windows.net")
        or not database_id.lower().endswith(expected_suffix)
        or subscription_match is None
    ):
        raise SystemExit("Terraform output does not identify the expected Sideyard staging database")

    account_output = run(
        ["az", "account", "show", "--output", "json"],
        cwd=root,
        environment=operator_environment,
        capture=True,
    )
    account = json.loads(account_output.stdout)
    subscription = subscription_match.group(1)
    if str(account.get("id", "")).lower() != subscription.lower():
        raise SystemExit("Azure CLI subscription does not match the staging Terraform state")
    tenant = str(account.get("tenantId", ""))
    if not re.fullmatch(r"[0-9a-fA-F-]{36}", tenant):
        raise SystemExit("Azure CLI did not report a valid tenant identifier")

    print(
        f"verified staging target: subscription={subscription} server={server} database=sideyard",
        flush=True,
    )
    with tempfile.TemporaryDirectory(prefix="sideyard-sql-operator-") as temporary:
        run(
            [
                "docker", "run", "--rm",
                "-e", "GOTOOLCHAIN=local", "-e", "CGO_ENABLED=0",
                "-v", "sideyard-go-modules:/go/pkg/mod",
                "-v", "sideyard-go-build:/root/.cache/go-build",
                "-v", f"{root}:/workspace:ro", "-v", f"{temporary}:/output",
                "-w", "/workspace", "golang:1.23-alpine",
                "go", "build", "-trimpath", "-o", "/output/clubdue", "./cmd/clubdue",
            ],
            cwd=root,
            environment=operator_environment,
        )
        command_environment = operator_environment.copy()
        for key in (
            "DATABASE_URL", "AZURE_SQL_CLIENT_ID", "AZURE_SQL_CLIENT_SECRET",
            "AZURE_SQL_RUNTIME_CLIENT_ID", "SESSION_SECRET", "PUBLIC_URL",
        ):
            command_environment.pop(key, None)
        command_environment.update(
            {
                "APP_ENV": "staging",
                "DATABASE_BACKEND": "azure_sql",
                "AZURE_SQL_SERVER": server,
                "AZURE_SQL_DATABASE": "sideyard",
                "AZURE_SQL_TENANT_ID": tenant,
                "AZURE_SQL_AUTH_MODE": "azure_cli",
            }
        )
        if args.runtime_client_id:
            command_environment["AZURE_SQL_RUNTIME_CLIENT_ID"] = args.runtime_client_id
        run(
            [str(Path(temporary) / "clubdue"), args.command],
            cwd=root,
            environment=command_environment,
        )


if __name__ == "__main__":
    main()
