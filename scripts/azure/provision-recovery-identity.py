#!/usr/bin/env python3
"""Manage staging-only Blob recovery identities from Terraform-bound resources."""

import argparse
from datetime import datetime, timedelta, timezone
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys


UUID = re.compile(r"^[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$")
PURPOSES = {
    "logical-backup-publisher": {
        "identity": "sideyard-staging-logical-backup-publisher",
        "container": "backups",
    },
    "logical-photo-reader": {
        "identity": "sideyard-staging-logical-photo-reader",
        "container": "photos",
    },
}


def run(command, *, cwd, environment=None):
    try:
        return subprocess.run(
            command, cwd=cwd, env=environment, check=True, text=True, capture_output=True
        )
    except subprocess.CalledProcessError as error:
        raise SystemExit(
            f"Recovery identity subprocess failed with exit status {error.returncode}"
        ) from None


def json_command(command, *, cwd, environment=None):
    try:
        return json.loads(run(command, cwd=cwd, environment=environment).stdout)
    except json.JSONDecodeError:
        raise SystemExit("Recovery identity subprocess returned invalid JSON") from None


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
    account_id = str(resources.get("storage_account_id", ""))
    subscription = re.match(
        r"^/subscriptions/([^/]+)/resourceGroups/[^/]+/providers/Microsoft\.Storage/storageAccounts/([^/]+)$",
        account_id,
        re.IGNORECASE,
    )
    if subscription is None:
        raise SystemExit("Terraform output does not identify the expected Sideyard staging storage")
    account_name = str(resources.get("storage_account", "")).lower()
    endpoint = str(resources.get("blob_endpoint", ""))
    expected_endpoint = f"https://{account_name}.blob.core.windows.net/"
    expected_backups = f"{account_id}/blobServices/default/containers/backups"
    expected_photos = f"{account_id}/blobServices/default/containers/photos"
    role_prefix = f"/subscriptions/{subscription.group(1)}/providers/Microsoft.Authorization/roleDefinitions/"
    if (
        resources.get("environment") != "staging"
        or subscription.group(2).lower() != account_name
        or not re.fullmatch(r"sideyard[a-z0-9]{1,16}", account_name)
        or endpoint.lower() != expected_endpoint.lower()
        or str(resources.get("backups_container_id", "")).lower() != expected_backups.lower()
        or str(resources.get("photos_container_id", "")).lower() != expected_photos.lower()
        or not str(resources.get("logical_backup_publisher_role_definition_id", "")).lower().startswith(role_prefix.lower())
        or not str(resources.get("logical_photo_reader_role_definition_id", "")).lower().startswith(role_prefix.lower())
    ):
        raise SystemExit("Terraform output does not identify the expected Sideyard staging storage")
    return resources, subscription.group(1), account_name


def require_private_directory(folder):
    if folder.exists() or folder.is_symlink():
        details = folder.lstat()
        if stat.S_ISLNK(details.st_mode) or not stat.S_ISDIR(details.st_mode):
            raise SystemExit("Recovery credential directory must be a directory, not a link")
        if details.st_uid != os.getuid() or stat.S_IMODE(details.st_mode) != 0o700:
            raise SystemExit("Recovery credential directory must be owner-only")
    else:
        folder.mkdir(parents=True, mode=0o700)


def read_credential_file(target):
    details = target.lstat()
    if stat.S_ISLNK(details.st_mode) or not stat.S_ISREG(details.st_mode):
        raise SystemExit("Recovery credential path must be a regular file, not a link")
    if details.st_uid != os.getuid() or stat.S_IMODE(details.st_mode) != 0o600:
        raise SystemExit("Recovery credential file must be owner-only")
    values = {}
    for line in target.read_text().splitlines():
        if not line or "=" not in line:
            raise SystemExit("Recovery credential file has an invalid line")
        key, value = line.split("=", 1)
        if key in values or not re.fullmatch(r"[A-Z][A-Z0-9_]+", key):
            raise SystemExit("Recovery credential file has an invalid or duplicate key")
        values[key] = value
    return values


def expected_values(*, purpose, resources, subscription, tenant, client_id, principal_id):
    details = PURPOSES[purpose]
    container = details["container"]
    return {
        "SIDEYARD_AZURE_ENVIRONMENT": "staging",
        "SIDEYARD_RECOVERY_PURPOSE": purpose,
        "AZURE_SUBSCRIPTION_ID": subscription,
        "AZURE_TENANT_ID": tenant,
        "AZURE_CLIENT_ID": client_id,
        "AZURE_PRINCIPAL_OBJECT_ID": principal_id,
        "AZURE_BLOB_ACCOUNT_URL": str(resources["blob_endpoint"]),
        "AZURE_BLOB_CONTAINER": container,
        "AZURE_BLOB_CONTAINER_RESOURCE_ID": str(resources[f"{container}_container_id"]),
    }


def validate_credential_values(
    values, *, purpose, resources, subscription, tenant, client_id, principal_id,
    require_fresh=True, now=None,
):
    expected = expected_values(
        purpose=purpose,
        resources=resources,
        subscription=subscription,
        tenant=tenant,
        client_id=client_id,
        principal_id=principal_id,
    )
    allowed = set(expected) | {
        "AZURE_CLIENT_SECRET",
        "AZURE_CREDENTIAL_KEY_ID",
        "AZURE_CREDENTIAL_EXPIRES",
    }
    if set(values) != allowed or any(values.get(key) != value for key, value in expected.items()):
        raise SystemExit("Recovery credential file does not match Terraform and Azure identity")
    if not values["AZURE_CLIENT_SECRET"] or not UUID.fullmatch(values["AZURE_CREDENTIAL_KEY_ID"]):
        raise SystemExit("Recovery credential file is incomplete")
    expiry = parse_timestamp(values["AZURE_CREDENTIAL_EXPIRES"], "stored credential expiry")
    if require_fresh and expiry <= (now or datetime.now(timezone.utc)) + timedelta(days=30):
        raise SystemExit("stored recovery credential expires within 30 days")


def write_credential_file(target, values):
    require_private_directory(target.parent)
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


def unlink_and_sync(target):
    target.unlink()
    directory = os.open(target.parent, os.O_RDONLY)
    try:
        os.fsync(directory)
    finally:
        os.close(directory)


def credential_by_key(credentials, key_id):
    matches = [
        item for item in credentials if str(item.get("keyId", "")).lower() == key_id.lower()
    ]
    return matches[0] if len(matches) == 1 else None


def reject_reused_local_identity(folder, target, client_id, principal_id):
    for candidate in folder.glob("staging-*.env"):
        if candidate == target:
            continue
        values = read_credential_file(candidate)
        other_client = values.get("AZURE_CLIENT_ID") or values.get("AZURE_SQL_CLIENT_ID")
        other_principal = values.get("AZURE_PRINCIPAL_OBJECT_ID")
        if other_client and other_client.lower() == client_id.lower():
            raise SystemExit("recovery client ID is already used by another protected identity")
        if other_principal and other_principal.lower() == principal_id.lower():
            raise SystemExit("recovery principal is already used by another protected identity")


def validate_role_assignments(assignments, *, purpose, resources, principal_id):
    expected_scope = str(resources[f"{PURPOSES[purpose]['container']}_container_id"])
    expected_role = str(resources[f"{purpose.replace('-', '_')}_role_definition_id"])
    if len(assignments) > 1:
        raise SystemExit("recovery principal has excess or inherited Azure role assignments")
    if assignments:
        assignment = assignments[0]
        if (
            str(assignment.get("principalId", "")).lower() != principal_id.lower()
            or str(assignment.get("scope", "")).lower() != expected_scope.lower()
            or str(assignment.get("roleDefinitionId", "")).lower() != expected_role.lower()
        ):
            raise SystemExit("recovery principal has an unexpected Azure role assignment")


def validate_role_definition(definitions, *, purpose, resources, subscription):
    if len(definitions) != 1:
        raise SystemExit("expected recovery role definition was not found exactly once")
    definition = definitions[0]
    role_id = str(resources[f"{purpose.replace('-', '_')}_role_definition_id"])
    expected_data = {
        "Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read"
    }
    if purpose == "logical-backup-publisher":
        expected_data.add("Microsoft.Storage/storageAccounts/blobServices/containers/blobs/write")
    permissions = definition.get("permissions") or []
    resource_group_scope = f"/subscriptions/{subscription}/resourceGroups/{resources['resource_group']}"
    if (
        str(definition.get("id", "")).lower() != role_id.lower()
        or len(permissions) != 1
        or permissions[0].get("actions") not in ([], None)
        or permissions[0].get("notActions") not in ([], None)
        or set(permissions[0].get("dataActions") or []) != expected_data
        or permissions[0].get("notDataActions") not in ([], None)
        or [scope.lower() for scope in definition.get("assignableScopes") or []]
        != [resource_group_scope.lower()]
    ):
        raise SystemExit("recovery role definition permissions or scope have drifted")


def require_empty_graph_page(page, label):
    if set(page) - {"@odata.context", "value"} or page.get("value") or page.get("@odata.nextLink"):
        raise SystemExit(f"recovery service principal has {label}")


def validate_live_credential(values, credentials, *, require_fresh, allow_expired=False):
    live = credential_by_key(credentials, values["AZURE_CREDENTIAL_KEY_ID"])
    if live is None:
        raise SystemExit("stored recovery credential key is absent from Azure")
    live_expiry = parse_timestamp(str(live.get("endDateTime", "")), "Azure credential expiry")
    stored_expiry = parse_timestamp(values["AZURE_CREDENTIAL_EXPIRES"], "stored credential expiry")
    if abs((live_expiry - stored_expiry).total_seconds()) > 1:
        raise SystemExit("stored recovery credential expiry does not match Azure")
    if live.get("displayName") != PURPOSES[values["SIDEYARD_RECOVERY_PURPOSE"]]["identity"]:
        raise SystemExit("stored recovery credential display name does not match its purpose")
    if not allow_expired and live_expiry <= datetime.now(timezone.utc):
        raise SystemExit("stored recovery credential has expired")
    if require_fresh and live_expiry <= datetime.now(timezone.utc) + timedelta(days=30):
        raise SystemExit("stored recovery credential expires within 30 days")


def delete_cloud_key(root, client_id, key_id):
    run(
        [
            "az",
            "ad",
            "app",
            "credential",
            "delete",
            "--id",
            client_id,
            "--key-id",
            key_id,
        ],
        cwd=root,
    )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("environment", choices=("staging",))
    parser.add_argument("purpose", choices=tuple(PURPOSES))
    parser.add_argument("--expires", help="ISO timestamp for a new or rotated credential")
    parser.add_argument("--expected-client-id", help="required to adopt an existing app on a new machine")
    action = parser.add_mutually_exclusive_group()
    action.add_argument("--rotate", action="store_true")
    action.add_argument("--revoke-key-id")
    action.add_argument("--rollback-rotation", action="store_true")
    assignment_gate = parser.add_mutually_exclusive_group(required=True)
    assignment_gate.add_argument("--require-unassigned", action="store_true")
    assignment_gate.add_argument("--require-assigned", action="store_true")
    parser.add_argument("--terraform-directory", type=Path)
    args = parser.parse_args()
    if args.expected_client_id and not UUID.fullmatch(args.expected_client_id):
        parser.error("--expected-client-id must be a UUID")
    if args.revoke_key_id and not UUID.fullmatch(args.revoke_key_id):
        parser.error("--revoke-key-id must be a UUID")
    if args.rotate and not args.expires:
        parser.error("--expires is required with --rotate")
    requested_expiry = parse_timestamp(args.expires, "--expires") if args.expires else None
    if requested_expiry and requested_expiry <= datetime.now(timezone.utc) + timedelta(days=30):
        parser.error("new recovery credential must remain valid for more than 30 days")

    purpose = args.purpose
    identity = PURPOSES[purpose]["identity"]
    root = Path(__file__).resolve().parents[2]
    terraform_directory = (
        args.terraform_directory.resolve()
        if args.terraform_directory
        else root / "infra" / "azure" / "environments"
    )
    resources, subscription, _ = terraform_resources(root, terraform_directory)
    account = json_command(["az", "account", "show", "--output", "json"], cwd=root)
    if str(account.get("id", "")).lower() != subscription.lower():
        raise SystemExit("Azure CLI subscription does not match the staging Terraform state")
    tenant = str(account.get("tenantId", ""))
    if not UUID.fullmatch(tenant):
        raise SystemExit("Azure CLI did not report a valid tenant identifier")

    folder = Path.home() / ".config" / "sideyard-azure"
    require_private_directory(folder)
    target = folder / f"staging-{purpose}.env"
    previous_target = target.with_name(target.name + ".previous")
    rollback_target = target.with_name(target.name + ".rollback")
    existing = read_credential_file(target) if target.exists() or target.is_symlink() else None
    if not existing and not args.expires and not args.revoke_key_id and not args.rollback_rotation:
        parser.error("--expires is required when creating a credential")
    local_client_id = existing.get("AZURE_CLIENT_ID") if existing else None
    if args.expected_client_id and local_client_id and args.expected_client_id.lower() != local_client_id.lower():
        raise SystemExit("expected client ID does not match the existing credential file")
    authorized_client_id = args.expected_client_id or local_client_id

    apps = json_command(
        ["az", "ad", "app", "list", "--display-name", identity, "--output", "json"], cwd=root
    )
    apps = [app for app in apps if app.get("displayName") == identity]
    if len(apps) > 1:
        raise SystemExit("multiple exact recovery applications exist")
    required_tags = {"SideyardEnvironment=staging", f"SideyardRecoveryPurpose={purpose}"}
    if apps:
        app = apps[0]
        if not authorized_client_id or str(app.get("appId", "")).lower() != authorized_client_id.lower():
            raise SystemExit("refusing to adopt an existing untracked recovery application")
        if not required_tags.issubset(set(app.get("tags") or [])):
            raise SystemExit("recovery application purpose tags do not match")
    else:
        if authorized_client_id:
            raise SystemExit("expected recovery application does not exist")
        app = json_command(
            [
                "az", "ad", "app", "create", "--display-name", identity,
                "--sign-in-audience", "AzureADMyOrg", "--tags", *sorted(required_tags),
                "--output", "json",
            ],
            cwd=root,
        )
    client_id = str(app.get("appId", ""))
    if not UUID.fullmatch(client_id):
        raise SystemExit("Azure application did not return a valid client identifier")
    app = json_command(["az", "ad", "app", "show", "--id", client_id, "--output", "json"], cwd=root)
    if (
        str(app.get("appId", "")).lower() != client_id.lower()
        or app.get("displayName") != identity
        or app.get("signInAudience") != "AzureADMyOrg"
        or not required_tags.issubset(set(app.get("tags") or []))
        or app.get("keyCredentials")
        or app.get("requiredResourceAccess")
    ):
        raise SystemExit("recovery application configuration is not single-tenant password-only")
    federated = json_command(
        ["az", "ad", "app", "federated-credential", "list", "--id", client_id, "--output", "json"],
        cwd=root,
    )
    if federated:
        raise SystemExit("recovery application must not have federated credentials")
    certificates = json_command(
        ["az", "ad", "app", "credential", "list", "--id", client_id, "--cert", "--output", "json"],
        cwd=root,
    )
    if certificates:
        raise SystemExit("recovery application must not have certificate credentials")
    principals = json_command(
        ["az", "ad", "sp", "list", "--filter", f"appId eq '{client_id}'", "--output", "json"],
        cwd=root,
    )
    if len(principals) > 1:
        raise SystemExit("multiple service principals exist for the recovery application")
    principal = principals[0] if principals else json_command(
        ["az", "ad", "sp", "create", "--id", client_id, "--output", "json"], cwd=root
    )
    principal_id = str(principal.get("id", ""))
    principal_app = str(principal.get("appId", ""))
    principal_tenant = str(principal.get("appOwnerOrganizationId", ""))
    if (
        not UUID.fullmatch(principal_id)
        or principal_app.lower() != client_id.lower()
        or principal_tenant.lower() != tenant.lower()
    ):
        raise SystemExit("Azure service principal does not match the expected app and tenant")
    principal = json_command(["az", "ad", "sp", "show", "--id", principal_id, "--output", "json"], cwd=root)
    if (
        str(principal.get("id", "")).lower() != principal_id.lower()
        or str(principal.get("appId", "")).lower() != client_id.lower()
        or str(principal.get("appOwnerOrganizationId", "")).lower() != tenant.lower()
        or principal.get("passwordCredentials")
        or principal.get("keyCredentials")
    ):
        raise SystemExit("recovery service principal has unexpected identity or credentials")
    for cert_flag in ([], ["--cert"]):
        direct_credentials = json_command(
            ["az", "ad", "sp", "credential", "list", "--id", principal_id, *cert_flag, "--output", "json"],
            cwd=root,
        )
        if direct_credentials:
            raise SystemExit("recovery service principal must not have direct credentials")
    graph_root = "https://graph.microsoft.com/v1.0"
    graph_checks = {
        "directory or group membership": f"{graph_root}/servicePrincipals/{principal_id}/memberOf?$select=id",
        "application role assignments": f"{graph_root}/servicePrincipals/{principal_id}/appRoleAssignments?$select=id",
        "delegated permission grants": f"{graph_root}/oauth2PermissionGrants?$filter=clientId%20eq%20'{principal_id}'&$select=id",
    }
    for label, url in graph_checks.items():
        page = json_command(["az", "rest", "--method", "get", "--url", url, "--output", "json"], cwd=root)
        require_empty_graph_page(page, label)
    reject_reused_local_identity(folder, target, client_id, principal_id)
    subscriptions = json_command(
        ["az", "account", "list", "--all", "--refresh", "--output", "json"], cwd=root
    )
    subscription_ids = {
        str(item.get("id", "")).lower()
        for item in subscriptions
        if str(item.get("tenantId", "")).lower() == tenant.lower() and item.get("state") == "Enabled"
    }
    if subscription.lower() not in subscription_ids or any(not UUID.fullmatch(item) for item in subscription_ids):
        raise SystemExit("Azure CLI subscription inventory is incomplete or invalid")
    assignments = []
    for subscription_id in sorted(subscription_ids):
        assignments.extend(json_command(
            [
                "az", "role", "assignment", "list", "--assignee-object-id", principal_id,
                "--subscription", subscription_id, "--all", "--include-inherited", "--include-groups",
                "--output", "json",
            ],
            cwd=root,
        ))
    validate_role_assignments(
        assignments, purpose=purpose, resources=resources, principal_id=principal_id
    )
    assignment_status = "assigned" if assignments else "unassigned"
    role_id = str(resources[f"{purpose.replace('-', '_')}_role_definition_id"])
    definitions = json_command(
        ["az", "role", "definition", "list", "--name", role_id, "--subscription", subscription, "--output", "json"],
        cwd=root,
    )
    validate_role_definition(
        definitions, purpose=purpose, resources=resources, subscription=subscription
    )
    if args.require_unassigned and assignments:
        raise SystemExit("recovery principal must be unassigned for this operation")
    if args.require_assigned and not assignments:
        raise SystemExit("recovery principal must have its exact assignment for this operation")

    credentials = json_command(
        ["az", "ad", "app", "credential", "list", "--id", client_id, "--output", "json"],
        cwd=root,
    )
    validation = dict(
        purpose=purpose,
        resources=resources,
        subscription=subscription,
        tenant=tenant,
        client_id=client_id,
        principal_id=principal_id,
    )
    previous = None
    if previous_target.exists() or previous_target.is_symlink():
        previous = read_credential_file(previous_target)
        validate_credential_values(previous, require_fresh=False, **validation)
    rollback = None
    if rollback_target.exists() or rollback_target.is_symlink():
        rollback = read_credential_file(rollback_target)
        validate_credential_values(rollback, require_fresh=False, **validation)
        if not args.rollback_rotation:
            raise SystemExit("credential rollback is incomplete and must be resumed")
    if existing:
        require_current_fresh = not (args.rotate or args.rollback_rotation or args.revoke_key_id)
        validate_credential_values(existing, require_fresh=require_current_fresh, **validation)
        records = [existing] + ([previous] if previous else []) + ([rollback] if rollback else [])
        protected_keys = {record["AZURE_CREDENTIAL_KEY_ID"].lower() for record in records}
        if not rollback and len(protected_keys) != len(records):
            raise SystemExit("saved prior credential is not a distinct rotation target")
        live_keys = [str(item.get("keyId", "")) for item in credentials]
        if any(not UUID.fullmatch(key) for key in live_keys):
            raise SystemExit("recovery application has an unsupported credential")
        live_key_set = {key.lower() for key in live_keys}
        if len(live_keys) != len(live_key_set) or not live_key_set.issubset(protected_keys):
            raise SystemExit("recovery application has untracked credentials")
        missing_protected = protected_keys - live_key_set
        rollback_key = rollback["AZURE_CREDENTIAL_KEY_ID"].lower() if rollback else None
        if missing_protected and not (
            args.rollback_rotation and rollback_key and missing_protected == {rollback_key}
        ):
            raise SystemExit("recovery application has untracked credentials")
        for record in records:
            key = record["AZURE_CREDENTIAL_KEY_ID"].lower()
            if key not in live_key_set:
                continue
            allow_expired = bool(
                (args.rotate and record is existing)
                or (args.revoke_key_id and key == args.revoke_key_id.lower())
                or (
                    args.rollback_rotation
                    and (
                        (rollback and key == rollback_key)
                        or (not rollback and previous and record is existing)
                    )
                )
            )
            validate_live_credential(
                record,
                credentials,
                require_fresh=require_current_fresh and record is existing,
                allow_expired=allow_expired,
            )
        if previous and not (args.rollback_rotation or args.revoke_key_id):
            raise SystemExit("credential rotation is pending canary, revocation, or rollback")
    elif credentials:
        raise SystemExit("refusing to adopt a recovery application with existing credentials")
    if args.rollback_rotation:
        if existing and not previous and not rollback and len(credentials) == 1:
            print(json.dumps({
                "identity": identity,
                "restored_key_id": existing["AZURE_CREDENTIAL_KEY_ID"],
                "rollback_status": "already_complete",
            }))
            return
        if not existing or (not previous and not rollback):
            raise SystemExit("current and saved rollback credentials are required for rollback")
        if not rollback:
            if credential_by_key(credentials, previous["AZURE_CREDENTIAL_KEY_ID"]) is None:
                raise SystemExit("saved prior credential is no longer active in Azure")
            write_credential_file(rollback_target, existing)
            rollback = existing
        rollback_key = rollback["AZURE_CREDENTIAL_KEY_ID"]
        restored_candidates = [
            record for record in (existing, previous)
            if record and record["AZURE_CREDENTIAL_KEY_ID"].lower() != rollback_key.lower()
        ]
        restored_keys = {record["AZURE_CREDENTIAL_KEY_ID"].lower() for record in restored_candidates}
        if len(restored_keys) != 1:
            raise SystemExit("rollback journal does not identify one prior credential")
        restored = restored_candidates[0]
        validate_live_credential(restored, credentials, require_fresh=False, allow_expired=False)
        write_credential_file(target, restored)
        if credential_by_key(credentials, rollback_key) is not None:
            try:
                delete_cloud_key(root, client_id, rollback_key)
            except BaseException:
                raise SystemExit(
                    f"rollback restored the prior credential; replacement key {rollback_key} still requires revocation"
                ) from None
        if previous_target.exists() or previous_target.is_symlink():
            unlink_and_sync(previous_target)
        unlink_and_sync(rollback_target)
        print(json.dumps({"identity": identity, "restored_key_id": restored["AZURE_CREDENTIAL_KEY_ID"], "revoked_replacement_key_id": rollback_key}))
        return
    if args.revoke_key_id:
        if not existing:
            raise SystemExit("a current local credential is required before revoking an old key")
        if args.revoke_key_id.lower() == existing["AZURE_CREDENTIAL_KEY_ID"].lower():
            raise SystemExit("refusing to revoke the current protected credential")
        if not previous:
            raise SystemExit("the saved prior credential is required before revoking an old key")
        if credential_by_key(credentials, args.revoke_key_id) is None:
            raise SystemExit("old credential key was not found exactly once")
        if args.revoke_key_id.lower() != previous["AZURE_CREDENTIAL_KEY_ID"].lower():
            raise SystemExit("only the exact saved prior credential may be revoked")
        delete_cloud_key(root, client_id, args.revoke_key_id)
        if previous_target.exists() or previous_target.is_symlink():
            previous = read_credential_file(previous_target)
            if previous.get("AZURE_CREDENTIAL_KEY_ID", "").lower() == args.revoke_key_id.lower():
                unlink_and_sync(previous_target)
        print(json.dumps({"identity": identity, "revoked_key_id": args.revoke_key_id}))
        return

    if existing and not args.rotate:
        pass
    else:
        expiry = requested_expiry
        if existing and not args.expected_client_id:
            raise SystemExit("rotation requires --expected-client-id as an explicit identity check")
        if existing:
            if previous_target.exists() or previous_target.is_symlink():
                raise SystemExit("complete or roll back the prior credential rotation first")
        credential = json_command(
            [
                "az", "ad", "app", "credential", "reset", "--id", client_id,
                "--append", "--display-name", identity, "--end-date", args.expires,
                "--output", "json",
            ],
            cwd=root,
        )
        secret = str(credential.get("password", ""))
        if not secret:
            raise SystemExit("Azure did not return the new recovery credential")
        key_id = None
        try:
            refreshed = json_command(
                ["az", "ad", "app", "credential", "list", "--id", client_id, "--output", "json"],
                cwd=root,
            )
            prior_keys = {str(item.get("keyId", "")).lower() for item in credentials if item.get("keyId")}
            candidates = [item for item in refreshed if str(item.get("keyId", "")).lower() not in prior_keys]
            if len(candidates) != 1:
                raise SystemExit("new Azure credential metadata could not be identified uniquely")
            current = candidates[0]
            key_id = str(current.get("keyId", ""))
            if (
                not UUID.fullmatch(key_id)
                or current.get("displayName") != identity
                or parse_timestamp(str(current.get("endDateTime", "")), "Azure credential expiry") != expiry
            ):
                raise SystemExit("new Azure credential metadata does not match the request")
            values = expected_values(**validation)
            values.update({
                "AZURE_CLIENT_SECRET": secret,
                "AZURE_CREDENTIAL_KEY_ID": key_id,
                "AZURE_CREDENTIAL_EXPIRES": current["endDateTime"],
            })
            if existing:
                write_credential_file(previous_target, existing)
            write_credential_file(target, values)
            validate_credential_values(read_credential_file(target), **validation)
        except BaseException:
            cloud_cleanup_failed = False
            local_rollback_failed = False
            if key_id:
                try:
                    delete_cloud_key(root, client_id, key_id)
                except BaseException:
                    cloud_cleanup_failed = True
            else:
                cloud_cleanup_failed = True
            if existing:
                try:
                    write_credential_file(target, existing)
                except BaseException:
                    local_rollback_failed = True
            if previous_target.exists() and not previous_target.is_symlink():
                try:
                    unlink_and_sync(previous_target)
                except BaseException:
                    local_rollback_failed = True
            if cloud_cleanup_failed:
                detail = key_id or "unknown"
                raise SystemExit(
                    f"new recovery key {detail} requires explicit Azure credential inventory cleanup"
                ) from None
            if local_rollback_failed:
                raise SystemExit(
                    "new recovery key was removed but the protected local credential requires repair"
                ) from None
            raise
        existing = values
        credentials = refreshed

    current_key = existing["AZURE_CREDENTIAL_KEY_ID"]
    stale_keys = [
        str(item.get("keyId")) for item in credentials
        if item.get("keyId") and str(item.get("keyId")).lower() != current_key.lower()
    ]
    print(json.dumps({
        "identity": identity,
        "purpose": purpose,
        "client_id": client_id,
        "principal_object_id": principal_id,
        "credential_file": str(target),
        "credential_key_id": current_key,
        "credential_expires": existing["AZURE_CREDENTIAL_EXPIRES"],
        "stale_key_ids": stale_keys,
        "container_resource_id": existing["AZURE_BLOB_CONTAINER_RESOURCE_ID"],
        "assignment_status": assignment_status,
        "role_assignment": "requires reviewed staging Terraform apply and permission canary" if not assignments else "exact assignment and role definition verified",
    }))


if __name__ == "__main__":
    main()
