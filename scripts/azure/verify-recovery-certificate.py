#!/usr/bin/env python3
"""Bind and verify certificate-only staging logical-recovery identities.

Entra application creation and certificate upload are deliberate owner actions in
Cloud Shell. This command never creates credentials and never needs Microsoft
Graph write access. It binds the returned non-secret IDs to protected local
certificate files, proves the private key matches the certificate, authenticates
the application, and audits its Azure RBAC assignment through the ARM-only
Terraform runner.
"""

import argparse
import base64
from datetime import datetime, timedelta, timezone
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import tempfile


UUID = re.compile(r"^[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$")
PURPOSES = {
    "logical-backup-publisher": "backups",
    "logical-photo-reader": "photos",
}


def run(command, *, cwd, environment=None, input_text=None):
    try:
        return subprocess.run(
            command,
            cwd=cwd,
            env=environment,
            input=input_text,
            check=True,
            text=True,
            capture_output=True,
        )
    except subprocess.CalledProcessError as error:
        raise SystemExit(
            f"Recovery certificate subprocess failed with exit status {error.returncode}"
        ) from None


def json_command(command, *, cwd, environment=None):
    try:
        return json.loads(run(command, cwd=cwd, environment=environment).stdout)
    except json.JSONDecodeError:
        raise SystemExit("Recovery certificate subprocess returned invalid JSON") from None


def parse_timestamp(value, label):
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        raise SystemExit(f"{label} must be an ISO timestamp") from None
    if parsed.tzinfo is None:
        raise SystemExit(f"{label} must include a timezone")
    return parsed.astimezone(timezone.utc)


def require_private_path(path, *, directory=False):
    details = path.lstat()
    wanted_type = stat.S_ISDIR if directory else stat.S_ISREG
    wanted_mode = 0o700 if directory else 0o600
    if stat.S_ISLNK(details.st_mode) or not wanted_type(details.st_mode):
        raise SystemExit(f"Recovery certificate path is not a safe {'directory' if directory else 'file'}")
    if details.st_uid != os.getuid() or stat.S_IMODE(details.st_mode) != wanted_mode:
        raise SystemExit(f"Recovery certificate {'directory' if directory else 'file'} must be owner-only")


def acquire_recovery_lock(folder):
    lock_path = folder / ".recovery-certificate.lock"
    flags = os.O_RDWR | os.O_CREAT | os.O_CLOEXEC
    if hasattr(os, "O_NOFOLLOW"):
        flags |= os.O_NOFOLLOW
    try:
        descriptor = os.open(lock_path, flags, 0o600)
    except OSError:
        raise SystemExit("Recovery certificate lock is unsafe or unavailable") from None
    handle = os.fdopen(descriptor, "r+")
    details = os.fstat(handle.fileno())
    if (
        not stat.S_ISREG(details.st_mode)
        or details.st_uid != os.getuid()
        or stat.S_IMODE(details.st_mode) != 0o600
    ):
        handle.close()
        raise SystemExit("Recovery certificate lock must be an owner-only regular file")
    fcntl.flock(handle.fileno(), fcntl.LOCK_EX)
    stale_profiles = [*folder.glob(".recovery-auth-*"), *folder.glob(".terraform-runner-*")]
    for candidate in stale_profiles:
        paths = [candidate, *candidate.rglob("*")] if candidate.is_dir() and not candidate.is_symlink() else [candidate]
        for path in paths:
            details = path.lstat()
            expected_mode = 0o700 if stat.S_ISDIR(details.st_mode) else 0o600
            if (
                stat.S_ISLNK(details.st_mode)
                or not (stat.S_ISDIR(details.st_mode) or stat.S_ISREG(details.st_mode))
                or details.st_uid != os.getuid()
                or stat.S_IMODE(details.st_mode) != expected_mode
            ):
                handle.close()
                raise SystemExit("Stale recovery authentication path is unsafe")
        shutil.rmtree(candidate)
    return handle


def terraform_resources(root, terraform_directory, environment=None):
    environment = dict(environment or os.environ)
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
    account_id = str(resources.get("storage_account_id", ""))
    match = re.match(
        r"^/subscriptions/([^/]+)/resourceGroups/sideyard-staging/providers/"
        r"Microsoft\.Storage/storageAccounts/(sideyard[a-z0-9]{1,16})$",
        account_id,
        re.IGNORECASE,
    )
    if match is None or resources.get("environment") != "staging":
        raise SystemExit("Terraform output does not identify Sideyard staging storage")
    runner_object_id = str(resources.get("terraform_runner_object_id", ""))
    if not UUID.fullmatch(runner_object_id):
        raise SystemExit("Terraform output does not identify the staging runner")
    account = match.group(2).lower()
    expected_endpoint = f"https://{account}.blob.core.windows.net/"
    expected = {
        "backups": f"{account_id}/blobServices/default/containers/backups",
        "photos": f"{account_id}/blobServices/default/containers/photos",
    }
    if (
        str(resources.get("storage_account", "")).lower() != account
        or str(resources.get("blob_endpoint", "")).lower() != expected_endpoint.lower()
        or any(str(resources.get(f"{name}_container_id", "")).lower() != value.lower() for name, value in expected.items())
    ):
        raise SystemExit("Terraform output does not identify Sideyard staging storage")
    return resources, match.group(1), account


def certificate_metadata(root, purpose):
    folder = Path.home() / ".config" / "sideyard-azure"
    require_private_path(folder, directory=True)
    prefix = folder / f"staging-{purpose}"
    key = Path(f"{prefix}-key.pem")
    cert = Path(f"{prefix}-cert.pem")
    combined = Path(f"{prefix}.pem")
    for path in (key, cert, combined):
        require_private_path(path)

    cert_public = run(
        ["openssl", "x509", "-in", str(cert), "-pubkey", "-noout"], cwd=root
    ).stdout
    key_public = run(
        ["openssl", "pkey", "-in", str(key), "-pubout"], cwd=root
    ).stdout
    if cert_public != key_public:
        raise SystemExit("Recovery certificate and private key do not match")
    if combined.read_bytes() != cert.read_bytes() + key.read_bytes():
        raise SystemExit("Combined recovery certificate file does not match its certificate and key")

    der = subprocess.run(
        ["openssl", "x509", "-in", str(cert), "-outform", "DER"],
        cwd=root,
        check=True,
        capture_output=True,
    ).stdout
    fingerprint = hashlib.sha256(der).hexdigest().upper()
    azure_thumbprint = hashlib.sha1(der).hexdigest().upper()
    end_line = run(["openssl", "x509", "-in", str(cert), "-enddate", "-noout"], cwd=root).stdout.strip()
    if not end_line.startswith("notAfter="):
        raise SystemExit("Recovery certificate expiry is unavailable")
    try:
        expiry = datetime.strptime(end_line.removeprefix("notAfter="), "%b %d %H:%M:%S %Y %Z").replace(tzinfo=timezone.utc)
    except ValueError:
        raise SystemExit("Recovery certificate expiry is invalid") from None
    if expiry <= datetime.now(timezone.utc) + timedelta(days=30):
        raise SystemExit("Recovery certificate expires within 30 days")
    return folder, key, cert, combined, fingerprint, azure_thumbprint, expiry


def expected_values(*, purpose, resources, subscription, tenant, client_id, principal_id, key_id, fingerprint, azure_thumbprint, expiry, key, cert, combined):
    container = PURPOSES[purpose]
    return {
        "SIDEYARD_AZURE_ENVIRONMENT": "staging",
        "SIDEYARD_RECOVERY_PURPOSE": purpose,
        "AZURE_SUBSCRIPTION_ID": subscription,
        "AZURE_TENANT_ID": tenant,
        "AZURE_CLIENT_ID": client_id,
        "AZURE_PRINCIPAL_OBJECT_ID": principal_id,
        "AZURE_CLIENT_CERTIFICATE_PATH": str(combined),
        "AZURE_CLIENT_PRIVATE_KEY_PATH": str(key),
        "AZURE_CLIENT_PUBLIC_CERTIFICATE_PATH": str(cert),
        "AZURE_CERTIFICATE_SHA256": fingerprint,
        "AZURE_CERTIFICATE_SHA1": azure_thumbprint,
        "AZURE_CREDENTIAL_KEY_ID": key_id,
        "AZURE_CREDENTIAL_EXPIRES": expiry.isoformat().replace("+00:00", "Z"),
        "AZURE_BLOB_ACCOUNT_URL": str(resources["blob_endpoint"]),
        "AZURE_BLOB_CONTAINER": container,
        "AZURE_BLOB_CONTAINER_RESOURCE_ID": str(resources[f"{container}_container_id"]),
    }


def read_values(target):
    require_private_path(target)
    values = {}
    for line in target.read_text().splitlines():
        if not line or "=" not in line:
            raise SystemExit("Recovery certificate binding has an invalid line")
        key, value = line.split("=", 1)
        if key in values or not re.fullmatch(r"[A-Z][A-Z0-9_]+", key):
            raise SystemExit("Recovery certificate binding has an invalid or duplicate key")
        values[key] = value
    return values


def write_values(target, values):
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


def authenticate_certificate(root, folder, values, environment=None):
    base_environment = dict(environment or os.environ)
    az = shutil.which("az", path=base_environment.get("PATH"))
    if not az:
        raise SystemExit("Azure CLI is required to verify the recovery certificate")
    with tempfile.TemporaryDirectory(prefix=".recovery-auth-", dir=folder) as config:
        os.chmod(config, 0o700)
        certificate = Path(config) / "identity.pem"
        certificate.write_bytes(Path(values["AZURE_CLIENT_CERTIFICATE_PATH"]).read_bytes())
        certificate.chmod(0o600)
        allowed = ("HOME", "USER", "LOGNAME", "TMPDIR", "LANG", "LC_ALL", "LC_CTYPE", "TERM", "XDG_RUNTIME_DIR", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy", "PATH")
        authentication_environment = {key: base_environment[key] for key in allowed if key in base_environment}
        authentication_environment["AZURE_CONFIG_DIR"] = str(config)
        reviewed_wrapper = Path(__file__).resolve().parent / "bin" / "az"
        run(
            [
                az,
                "login",
                "--service-principal",
                "--username",
                values["AZURE_CLIENT_ID"],
                "--certificate",
                "/azure/identity.pem" if Path(az).resolve() == reviewed_wrapper else str(certificate),
                "--tenant",
                values["AZURE_TENANT_ID"],
                "--allow-no-subscriptions",
                "--output",
                "none",
            ],
            cwd=config,
            environment=authentication_environment,
        )
        token = json_command(
            [az, "account", "get-access-token", "--resource", "https://storage.azure.com/", "--output", "json"],
            cwd=config,
            environment=authentication_environment,
        )
        expires = token.get("expires_on") or token.get("expiresOn")
        encoded = str(token.get("accessToken", ""))
        parts = encoded.split(".")
        if len(parts) != 3:
            raise SystemExit("Recovery certificate returned an invalid Blob access token")
        try:
            payload = json.loads(base64.urlsafe_b64decode(parts[1] + "=" * (-len(parts[1]) % 4)))
        except (ValueError, json.JSONDecodeError):
            raise SystemExit("Recovery certificate returned an invalid Blob access token") from None
        now = datetime.now(timezone.utc).timestamp()
        application = str(payload.get("azp") or payload.get("appid") or "")
        audiences = payload.get("aud")
        if isinstance(audiences, str):
            audiences = [audiences]
        if (
            token.get("tokenType") != "Bearer"
            or not expires
            or application.lower() != values["AZURE_CLIENT_ID"].lower()
            or str(payload.get("oid", "")).lower() != values["AZURE_PRINCIPAL_OBJECT_ID"].lower()
            or str(payload.get("tid", "")).lower() != values["AZURE_TENANT_ID"].lower()
            or not any(str(audience).rstrip("/") == "https://storage.azure.com" for audience in (audiences or []))
            or not isinstance(payload.get("nbf"), (int, float))
            or not isinstance(payload.get("exp"), (int, float))
            or payload["nbf"] > now + 60
            or payload["exp"] < now + 55 * 60
            or payload["exp"] > now + 2 * 60 * 60
        ):
            raise SystemExit("Recovery certificate did not return a bounded Blob access token")
        return encoded


def audit_rbac(root, resources, subscription, purpose, principal_id, require_assigned, environment=None):
    if principal_id.lower() == str(resources["terraform_runner_object_id"]).lower():
        raise SystemExit("Recovery principal must not reuse the Terraform runner")
    subscriptions = json_command(["az", "account", "list", "--all", "--refresh", "--output", "json"], cwd=root, environment=environment)
    enabled = {
        str(item.get("id", "")).lower()
        for item in subscriptions
        if item.get("state") == "Enabled"
    }
    if subscription.lower() not in enabled:
        raise SystemExit("Terraform subscription is absent from the runner inventory")
    assignments = []
    for subscription_id in sorted(enabled):
        scoped = json_command(
            [
                "az", "role", "assignment", "list",
                "--assignee-object-id", principal_id,
                "--subscription", subscription_id,
                "--all", "--include-inherited", "--include-groups",
                "--fill-principal-name", "false",
                "--fill-role-definition-name", "false",
                "--output", "json",
            ],
            cwd=root, environment=environment,
        )
        inherited = json_command(
            [
                "az", "role", "assignment", "list",
                "--assignee-object-id", principal_id,
                "--subscription", subscription_id,
                "--scope", f"/subscriptions/{subscription_id}",
                "--include-inherited",
                "--fill-principal-name", "false",
                "--fill-role-definition-name", "false",
                "--output", "json",
            ],
            cwd=root, environment=environment,
        )
        assignments.extend(scoped)
        assignments.extend(inherited)
    unique_assignments = {}
    for assignment in assignments:
        identity = str(assignment.get("id", "")).lower() or "|".join(
            str(assignment.get(field, "")).lower()
            for field in ("principalId", "scope", "roleDefinitionId")
        )
        unique_assignments[identity] = assignment
    assignments = list(unique_assignments.values())
    container = PURPOSES[purpose]
    expected_scope = str(resources[f"{container}_container_id"])
    role_id = str(resources[f"{purpose.replace('-', '_')}_role_definition_id"])
    if len(assignments) > 1:
        raise SystemExit("Recovery principal has excess or inherited Azure role assignments")
    if assignments:
        assignment = assignments[0]
        if (
            str(assignment.get("principalId", "")).lower() != principal_id.lower()
            or str(assignment.get("scope", "")).lower() != expected_scope.lower()
            or str(assignment.get("roleDefinitionId", "")).lower() != role_id.lower()
        ):
            raise SystemExit("Recovery principal has an unexpected Azure role assignment")
    if require_assigned != bool(assignments):
        wanted = "assigned" if require_assigned else "unassigned"
        raise SystemExit(f"Recovery principal must be {wanted} for this operation")

    role_uuid = role_id.rsplit("/", 1)[-1]
    if not UUID.fullmatch(role_uuid):
        raise SystemExit("Recovery role definition ID is invalid")
    definitions = json_command(
        ["az", "role", "definition", "list", "--name", role_uuid, "--subscription", subscription, "--output", "json"],
        cwd=root, environment=environment,
    )
    expected_actions = {"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read"}
    if purpose == "logical-backup-publisher":
        expected_actions.add("Microsoft.Storage/storageAccounts/blobServices/containers/blobs/write")
    expected_role_scope = f"/subscriptions/{subscription}/resourceGroups/{resources['resource_group']}"
    if len(definitions) != 1:
        raise SystemExit("Recovery role definition was not found exactly once")
    definition = definitions[0]
    permissions = definition.get("permissions") or []
    if (
        str(definition.get("id", "")).lower() != role_id.lower()
        or [str(scope).lower() for scope in definition.get("assignableScopes") or []] != [expected_role_scope.lower()]
        or len(permissions) != 1
        or permissions[0].get("actions") not in ([], None)
        or permissions[0].get("notActions") not in ([], None)
        or set(permissions[0].get("dataActions") or []) != expected_actions
        or permissions[0].get("notDataActions") not in ([], None)
    ):
        raise SystemExit("Recovery role definition permissions or scope have drifted")
    return "assigned" if assignments else "unassigned"


def authenticate_existing_identity(root, resources, subscription, tenant, purpose, audit_environment=None):
    """Revalidate one protected binding and return a short-lived Blob token.

    The caller must hold the recovery-certificate lock for the full call and while
    constructing the container-scoped client that consumes the returned token.
    """
    if purpose not in PURPOSES or not UUID.fullmatch(subscription) or not UUID.fullmatch(tenant):
        raise SystemExit("Recovery identity context is invalid")
    folder, key, cert, combined, fingerprint, azure_thumbprint, certificate_expiry = certificate_metadata(root, purpose)
    target = folder / f"staging-{purpose}.env"
    if not target.exists():
        raise SystemExit("Protected recovery certificate binding is missing")
    existing = read_values(target)
    declared_expiry = parse_timestamp(existing.get("AZURE_CREDENTIAL_EXPIRES", ""), "stored credential expiry")
    if abs((declared_expiry - certificate_expiry).total_seconds()) > 1:
        raise SystemExit("Azure credential expiry does not match the local certificate")
    if existing.get("AZURE_CERTIFICATE_SHA1") != azure_thumbprint:
        raise SystemExit("Azure credential thumbprint does not match the local certificate")
    values = expected_values(
        purpose=purpose,
        resources=resources,
        subscription=subscription,
        tenant=tenant,
        client_id=existing.get("AZURE_CLIENT_ID", ""),
        principal_id=existing.get("AZURE_PRINCIPAL_OBJECT_ID", ""),
        key_id=existing.get("AZURE_CREDENTIAL_KEY_ID", ""),
        fingerprint=fingerprint,
        azure_thumbprint=azure_thumbprint,
        expiry=certificate_expiry,
        key=key,
        cert=cert,
        combined=combined,
    )
    if existing != values:
        raise SystemExit("Protected recovery certificate binding has drifted")
    audit_rbac(root, resources, subscription, purpose, values["AZURE_PRINCIPAL_OBJECT_ID"], True, environment=audit_environment)
    return values, authenticate_certificate(root, folder, values, audit_environment)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("environment", choices=("staging",))
    parser.add_argument("purpose", choices=tuple(PURPOSES))
    parser.add_argument("--client-id")
    parser.add_argument("--principal-object-id")
    parser.add_argument("--credential-key-id")
    parser.add_argument("--credential-expires")
    parser.add_argument("--credential-thumbprint")
    gate = parser.add_mutually_exclusive_group(required=True)
    gate.add_argument("--require-unassigned", action="store_true")
    gate.add_argument("--require-assigned", action="store_true")
    parser.add_argument("--terraform-directory", type=Path)
    args = parser.parse_args()

    supplied = (
        args.client_id,
        args.principal_object_id,
        args.credential_key_id,
        args.credential_expires,
        args.credential_thumbprint,
    )
    if any(supplied) and not all(supplied):
        parser.error("all certificate binding identifiers must be supplied together")
    for label, value in zip(("client ID", "principal object ID", "credential key ID"), supplied[:3]):
        if value and not UUID.fullmatch(value):
            parser.error(f"{label} must be a UUID")

    root = Path(__file__).resolve().parents[2]
    terraform_directory = args.terraform_directory.resolve() if args.terraform_directory else root / "infra" / "azure" / "environments"
    resources, subscription, _ = terraform_resources(root, terraform_directory)
    account = json_command(["az", "account", "show", "--output", "json"], cwd=root)
    tenant = str(account.get("tenantId", ""))
    if str(account.get("id", "")).lower() != subscription.lower() or not UUID.fullmatch(tenant):
        raise SystemExit("Azure runner does not match staging Terraform state")

    folder = Path.home() / ".config" / "sideyard-azure"
    require_private_path(folder, directory=True)
    recovery_lock = acquire_recovery_lock(folder)
    try:
        folder, key, cert, combined, fingerprint, azure_thumbprint, certificate_expiry = certificate_metadata(root, args.purpose)
        target = folder / f"staging-{args.purpose}.env"
        existing = read_values(target) if target.exists() else None
        if existing is None and not all(supplied):
            parser.error("Cloud Shell certificate IDs are required for the initial binding")

        if existing:
            client_id = existing.get("AZURE_CLIENT_ID", "")
            principal_id = existing.get("AZURE_PRINCIPAL_OBJECT_ID", "")
            key_id = existing.get("AZURE_CREDENTIAL_KEY_ID", "")
            declared_expiry = parse_timestamp(existing.get("AZURE_CREDENTIAL_EXPIRES", ""), "stored credential expiry")
            if all(supplied) and tuple(value.lower() for value in supplied[:3]) != (client_id.lower(), principal_id.lower(), key_id.lower()):
                raise SystemExit("Supplied certificate IDs do not match the protected binding")
            if args.credential_expires and parse_timestamp(args.credential_expires, "credential expiry") != declared_expiry:
                raise SystemExit("Supplied credential expiry does not match the protected binding")
            if args.credential_thumbprint and args.credential_thumbprint != existing.get("AZURE_CERTIFICATE_SHA1"):
                raise SystemExit("Supplied credential thumbprint does not match the protected binding")
        else:
            client_id, principal_id, key_id = supplied[:3]
            declared_expiry = parse_timestamp(args.credential_expires, "credential expiry")

        if abs((declared_expiry - certificate_expiry).total_seconds()) > 1:
            raise SystemExit("Azure credential expiry does not match the local certificate")
        declared_thumbprint = args.credential_thumbprint if existing is None else existing.get("AZURE_CERTIFICATE_SHA1")
        if declared_thumbprint != azure_thumbprint:
            raise SystemExit("Azure credential thumbprint does not match the local certificate")
        values = expected_values(
            purpose=args.purpose,
            resources=resources,
            subscription=subscription,
            tenant=tenant,
            client_id=client_id,
            principal_id=principal_id,
            key_id=key_id,
            fingerprint=fingerprint,
            azure_thumbprint=azure_thumbprint,
            expiry=certificate_expiry,
            key=key,
            cert=cert,
            combined=combined,
        )
        if existing and existing != values:
            raise SystemExit("Protected recovery certificate binding has drifted")

        assignment_status = audit_rbac(
            root,
            resources,
            subscription,
            args.purpose,
            principal_id,
            args.require_assigned,
        )
        authenticate_certificate(root, folder, values)
        if existing is None:
            write_values(target, values)

        print(json.dumps({
            "identity": f"sideyard-staging-{args.purpose}",
            "purpose": args.purpose,
            "client_id": client_id,
            "principal_object_id": principal_id,
            "credential_key_id": key_id,
            "credential_expires": values["AZURE_CREDENTIAL_EXPIRES"],
            "certificate_sha256": fingerprint,
            "credential_file": str(target),
            "container_resource_id": values["AZURE_BLOB_CONTAINER_RESOURCE_ID"],
            "assignment_status": assignment_status,
        }))
    finally:
        recovery_lock.close()


if __name__ == "__main__":
    main()
