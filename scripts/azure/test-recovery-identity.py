#!/usr/bin/env python3
"""Focused tests for staging recovery identities and their Terraform scopes."""

from contextlib import redirect_stderr, redirect_stdout
from datetime import datetime, timedelta, timezone
import importlib.util
import io
import json
import os
from pathlib import Path
import stat
import sys
import tempfile
import unittest
from unittest import mock


ROOT = Path(__file__).resolve().parents[2]
SOURCE = Path(__file__).with_name("provision-recovery-identity.py")
SPEC = importlib.util.spec_from_file_location("recovery_identity", SOURCE)
IDENTITY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(IDENTITY)

SUBSCRIPTION = "11111111-1111-1111-1111-111111111111"
TENANT = "22222222-2222-2222-2222-222222222222"
CLIENT = "33333333-3333-3333-3333-333333333333"
PRINCIPAL = "44444444-4444-4444-4444-444444444444"
KEY = "55555555-5555-5555-5555-555555555555"
OLD_KEY = "66666666-6666-6666-6666-666666666666"
ACCOUNT = "sideyardtestaccount"
ACCOUNT_ID = (
    f"/subscriptions/{SUBSCRIPTION}/resourceGroups/sideyard-staging/"
    f"providers/Microsoft.Storage/storageAccounts/{ACCOUNT}"
)
EXPIRY = (datetime.now(timezone.utc) + timedelta(days=90)).replace(microsecond=0)
EXPIRY_TEXT = EXPIRY.isoformat().replace("+00:00", "Z")


def resources():
    return {
        "environment": "staging",
        "resource_group": "sideyard-staging",
        "storage_account": ACCOUNT,
        "storage_account_id": ACCOUNT_ID,
        "blob_endpoint": f"https://{ACCOUNT}.blob.core.windows.net/",
        "backups_container_id": f"{ACCOUNT_ID}/blobServices/default/containers/backups",
        "photos_container_id": f"{ACCOUNT_ID}/blobServices/default/containers/photos",
        "logical_backup_publisher_role_definition_id": f"/subscriptions/{SUBSCRIPTION}/providers/Microsoft.Authorization/roleDefinitions/88888888-8888-8888-8888-888888888888",
        "logical_photo_reader_role_definition_id": f"/subscriptions/{SUBSCRIPTION}/providers/Microsoft.Authorization/roleDefinitions/99999999-9999-9999-9999-999999999999",
    }


def app(purpose="logical-backup-publisher"):
    return {
        "displayName": f"sideyard-staging-{purpose}",
        "appId": CLIENT,
        "tags": ["SideyardEnvironment=staging", f"SideyardRecoveryPurpose={purpose}"],
        "signInAudience": "AzureADMyOrg",
        "keyCredentials": [],
    }


def principal():
    return {
        "id": PRINCIPAL,
        "appId": CLIENT,
        "appOwnerOrganizationId": TENANT,
        "passwordCredentials": [],
        "keyCredentials": [],
    }


def credential(key=KEY, purpose="logical-backup-publisher", expiry=EXPIRY_TEXT):
    return {
        "keyId": key,
        "displayName": f"sideyard-staging-{purpose}",
        "startDateTime": "2026-01-02T00:00:00Z" if key == KEY else "2026-01-01T00:00:00Z",
        "endDateTime": expiry,
    }


def role_definition(purpose="logical-backup-publisher"):
    data_actions = ["Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read"]
    if purpose == "logical-backup-publisher":
        data_actions.append("Microsoft.Storage/storageAccounts/blobServices/containers/blobs/write")
    return {
        "id": resources()[f"{purpose.replace('-', '_')}_role_definition_id"],
        "assignableScopes": [f"/subscriptions/{SUBSCRIPTION}/resourceGroups/sideyard-staging"],
        "permissions": [{
            "actions": [],
            "notActions": [],
            "dataActions": data_actions,
            "notDataActions": [],
        }],
    }


class RecoveryIdentityTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.home = Path(self.temp.name)
        self.tf = self.home / "tf"
        (self.tf / ".terraform" / "staging").mkdir(parents=True)
        (self.tf / "staging.local.hcl").write_text("container_name = \"state\"\n")

    def tearDown(self):
        self.temp.cleanup()

    def invoke(
        self, purpose="logical-backup-publisher", extra=(), handler=None, run_handler=None,
        app_detail=None, assignments=None, federated=None, certificates=None,
        sp_passwords=None, sp_certificates=None, graph_values=None,
        role_definitions=None, subscriptions=None,
    ):
        argv = [
            str(SOURCE), "staging", purpose, "--terraform-directory", str(self.tf), *extra
        ]
        if "--require-assigned" not in extra and "--require-unassigned" not in extra:
            argv.append("--require-assigned" if assignments else "--require-unassigned")
        output, errors = io.StringIO(), io.StringIO()
        def routed(command, **kwargs):
            joined = " ".join(command)
            if "app show" in joined:
                return app_detail if app_detail is not None else app(purpose)
            if "sp show" in joined:
                return principal()
            if "sp credential list" in joined:
                if "--cert" in command:
                    return sp_certificates if sp_certificates is not None else []
                return sp_passwords if sp_passwords is not None else []
            if "federated-credential list" in joined:
                return federated if federated is not None else []
            if "credential list" in joined and "--cert" in command:
                return certificates if certificates is not None else []
            if "role assignment list" in joined:
                if isinstance(assignments, dict):
                    subscription_id = command[command.index("--subscription") + 1]
                    return assignments.get(subscription_id, [])
                return assignments if assignments is not None else []
            if "role definition list" in joined:
                return role_definitions if role_definitions is not None else [role_definition(purpose)]
            if "account list" in joined:
                return subscriptions if subscriptions is not None else [{"id": SUBSCRIPTION, "tenantId": TENANT, "state": "Enabled"}]
            if command[:2] == ["az", "rest"]:
                return {"@odata.context": "context", "value": graph_values or []}
            return handler(command, **kwargs)

        with (
            mock.patch.object(sys, "argv", argv),
            mock.patch.object(IDENTITY.Path, "home", return_value=self.home),
            mock.patch.object(
                IDENTITY, "terraform_resources", return_value=(resources(), SUBSCRIPTION, ACCOUNT)
            ),
            mock.patch.object(IDENTITY, "json_command", side_effect=routed) as json_mock,
            mock.patch.object(IDENTITY, "run", side_effect=run_handler) as run_mock,
            redirect_stdout(output),
            redirect_stderr(errors),
        ):
            IDENTITY.main()
        return output.getvalue(), errors.getvalue(), json_mock, run_mock

    def creation_handler(self, purpose="logical-backup-publisher"):
        calls = {"credentials": 0}

        def handler(command, **_):
            joined = " ".join(command)
            if "account show" in joined:
                return {"id": SUBSCRIPTION, "tenantId": TENANT}
            if "app list" in joined:
                return []
            if "app create" in joined:
                return app(purpose)
            if "sp list" in joined:
                return []
            if "sp create" in joined:
                return principal()
            if "credential reset" in joined:
                return {"password": "SENTINEL-SECRET"}
            if "credential list" in joined:
                calls["credentials"] += 1
                return [] if calls["credentials"] == 1 else [credential(purpose=purpose)]
            self.fail(f"unexpected command: {command}")

        return handler

    def existing_handler(self, purpose="logical-backup-publisher", credentials=None, tags=True):
        application = app(purpose)
        if not tags:
            application["tags"] = []

        def handler(command, **_):
            joined = " ".join(command)
            if "account show" in joined:
                return {"id": SUBSCRIPTION, "tenantId": TENANT}
            if "app list" in joined:
                return [application]
            if "sp list" in joined:
                return [principal()]
            if "credential list" in joined:
                return credentials if credentials is not None else [credential(purpose=purpose)]
            self.fail(f"unexpected command: {command}")

        return handler

    def create_file(self, purpose="logical-backup-publisher", key=KEY, secret="SENTINEL-SECRET"):
        folder = self.home / ".config" / "sideyard-azure"
        folder.mkdir(parents=True, mode=0o700, exist_ok=True)
        values = IDENTITY.expected_values(
            purpose=purpose,
            resources=resources(),
            subscription=SUBSCRIPTION,
            tenant=TENANT,
            client_id=CLIENT,
            principal_id=PRINCIPAL,
        )
        values.update({
            "AZURE_CLIENT_SECRET": secret,
            "AZURE_CREDENTIAL_KEY_ID": key,
            "AZURE_CREDENTIAL_EXPIRES": EXPIRY_TEXT,
        })
        target = folder / f"staging-{purpose}.env"
        target.write_text("".join(f"{name}={value}\n" for name, value in values.items()))
        target.chmod(0o600)
        return target, values

    def test_create_binds_file_and_never_prints_secret(self):
        output, errors, _, _ = self.invoke(
            extra=("--expires", EXPIRY_TEXT), handler=self.creation_handler()
        )
        result = json.loads(output)
        target = Path(result["credential_file"])
        self.assertEqual(stat.S_IMODE(target.stat().st_mode), 0o600)
        self.assertEqual(stat.S_IMODE(target.parent.stat().st_mode), 0o700)
        self.assertNotIn("SENTINEL-SECRET", output + errors)
        values = IDENTITY.read_credential_file(target)
        self.assertEqual(values["AZURE_BLOB_CONTAINER"], "backups")
        self.assertEqual(values["AZURE_PRINCIPAL_OBJECT_ID"], PRINCIPAL)

    def test_create_photo_reader_uses_separate_name_file_and_container(self):
        purpose = "logical-photo-reader"
        output, _, _, _ = self.invoke(
            purpose, ("--expires", EXPIRY_TEXT), self.creation_handler(purpose)
        )
        result = json.loads(output)
        values = IDENTITY.read_credential_file(Path(result["credential_file"]))
        self.assertEqual(result["identity"], "sideyard-staging-logical-photo-reader")
        self.assertEqual(values["AZURE_BLOB_CONTAINER"], "photos")
        self.assertIn("/containers/photos", values["AZURE_BLOB_CONTAINER_RESOURCE_ID"])

    def test_existing_app_requires_explicit_or_protected_client_id(self):
        with self.assertRaisesRegex(SystemExit, "untracked"):
            self.invoke(
                extra=("--expires", EXPIRY_TEXT), handler=self.existing_handler(credentials=[])
            )

    def test_existing_app_requires_purpose_tags(self):
        self.create_file()
        with self.assertRaisesRegex(SystemExit, "purpose tags"):
            self.invoke(handler=self.existing_handler(tags=False))

    def test_duplicate_application_and_principal_are_rejected(self):
        def duplicate_apps(command, **_):
            joined = " ".join(command)
            if "account show" in joined:
                return {"id": SUBSCRIPTION, "tenantId": TENANT}
            if "app list" in joined:
                return [app(), app()]
            self.fail(f"unexpected command: {command}")

        with self.assertRaisesRegex(SystemExit, "multiple exact recovery applications"):
            self.invoke(extra=("--expires", EXPIRY_TEXT), handler=duplicate_apps)

        def duplicate_principals(command, **_):
            joined = " ".join(command)
            if "account show" in joined:
                return {"id": SUBSCRIPTION, "tenantId": TENANT}
            if "app list" in joined:
                return [app()]
            if "sp list" in joined:
                return [principal(), principal()]
            self.fail(f"unexpected command: {command}")

        with self.assertRaisesRegex(SystemExit, "multiple service principals"):
            self.invoke(
                extra=("--expected-client-id", CLIENT, "--expires", EXPIRY_TEXT),
                handler=duplicate_principals,
            )

    def test_service_principal_must_match_app_and_tenant(self):
        wrong = principal()
        wrong["appOwnerOrganizationId"] = "77777777-7777-7777-7777-777777777777"

        def handler(command, **_):
            joined = " ".join(command)
            if "account show" in joined:
                return {"id": SUBSCRIPTION, "tenantId": TENANT}
            if "app list" in joined:
                return [app()]
            if "sp list" in joined:
                return [wrong]
            self.fail(f"unexpected command: {command}")

        with self.assertRaisesRegex(SystemExit, "expected app and tenant"):
            self.invoke(
                extra=("--expected-client-id", CLIENT, "--expires", EXPIRY_TEXT),
                handler=handler,
            )

    def test_application_must_be_single_tenant_without_other_auth_methods(self):
        self.create_file()
        multi_tenant = app()
        multi_tenant["signInAudience"] = "AzureADMultipleOrgs"
        with self.assertRaisesRegex(SystemExit, "single-tenant password-only"):
            self.invoke(handler=self.existing_handler(), app_detail=multi_tenant)
        with self.assertRaisesRegex(SystemExit, "certificate credentials"):
            self.invoke(handler=self.existing_handler(), certificates=[{"keyId": OLD_KEY}])
        with self.assertRaisesRegex(SystemExit, "federated credentials"):
            self.invoke(handler=self.existing_handler(), federated=[{"id": "subject"}])

    def test_service_principal_credentials_and_directory_grants_are_rejected(self):
        self.create_file()
        with self.assertRaisesRegex(SystemExit, "direct credentials"):
            self.invoke(handler=self.existing_handler(), sp_passwords=[{"keyId": OLD_KEY}])
        with self.assertRaisesRegex(SystemExit, "direct credentials"):
            self.invoke(handler=self.existing_handler(), sp_certificates=[{"keyId": OLD_KEY}])
        with self.assertRaisesRegex(SystemExit, "directory or group membership"):
            self.invoke(handler=self.existing_handler(), graph_values=[{"id": "grant"}])

    def test_existing_file_and_live_key_are_verified(self):
        self.create_file()
        output, _, _, _ = self.invoke(handler=self.existing_handler())
        self.assertEqual(json.loads(output)["credential_key_id"], KEY)

    def test_untracked_password_credential_is_rejected(self):
        self.create_file()
        with self.assertRaisesRegex(SystemExit, "untracked credentials"):
            self.invoke(
                handler=self.existing_handler(credentials=[credential(KEY), credential(OLD_KEY)])
            )

    def test_effective_role_assignment_must_match_exact_allowlist(self):
        self.create_file()
        expected = {
            "principalId": PRINCIPAL,
            "scope": resources()["backups_container_id"],
            "roleDefinitionId": resources()["logical_backup_publisher_role_definition_id"],
        }
        output, _, _, _ = self.invoke(handler=self.existing_handler(), assignments=[expected])
        self.assertEqual(json.loads(output)["principal_object_id"], PRINCIPAL)
        wrong = dict(expected)
        wrong["scope"] = resources()["photos_container_id"]
        with self.assertRaisesRegex(SystemExit, "unexpected Azure role assignment"):
            self.invoke(handler=self.existing_handler(), assignments=[wrong])
        with self.assertRaisesRegex(SystemExit, "excess or inherited"):
            self.invoke(handler=self.existing_handler(), assignments=[expected, expected])

    def test_assignment_gate_and_live_role_permissions_are_enforced(self):
        self.create_file()
        expected = {
            "principalId": PRINCIPAL,
            "scope": resources()["backups_container_id"],
            "roleDefinitionId": resources()["logical_backup_publisher_role_definition_id"],
        }
        with self.assertRaisesRegex(SystemExit, "must be unassigned"):
            self.invoke(
                extra=("--require-unassigned",),
                handler=self.existing_handler(),
                assignments=[expected],
            )
        with self.assertRaisesRegex(SystemExit, "must have its exact assignment"):
            self.invoke(
                extra=("--require-assigned",), handler=self.existing_handler(), assignments=[]
            )
        drifted = role_definition()
        drifted["permissions"][0]["dataActions"].append(
            "Microsoft.Storage/storageAccounts/blobServices/containers/blobs/delete"
        )
        with self.assertRaisesRegex(SystemExit, "permissions or scope have drifted"):
            self.invoke(
                handler=self.existing_handler(), assignments=[expected], role_definitions=[drifted]
            )

    def test_role_inventory_covers_every_enabled_tenant_subscription(self):
        self.assertIn('"account", "list", "--all", "--refresh"', SOURCE.read_text())
        self.create_file()
        other_subscription = "aaaaaaaa-1111-1111-1111-111111111111"
        subscription_rows = [
            {"id": SUBSCRIPTION, "tenantId": TENANT, "state": "Enabled"},
            {"id": other_subscription, "tenantId": TENANT, "state": "Enabled"},
        ]
        other_role = {
            "principalId": PRINCIPAL,
            "scope": f"/subscriptions/{other_subscription}",
            "roleDefinitionId": "/subscriptions/aaaaaaaa-1111-1111-1111-111111111111/providers/Microsoft.Authorization/roleDefinitions/bbbbbbbb-1111-1111-1111-111111111111",
        }
        with self.assertRaisesRegex(SystemExit, "unexpected Azure role assignment"):
            self.invoke(
                handler=self.existing_handler(),
                subscriptions=subscription_rows,
                assignments={SUBSCRIPTION: [], other_subscription: [other_role]},
            )

    def test_protected_identity_cannot_be_reused_for_another_purpose(self):
        self.create_file("logical-photo-reader")
        with self.assertRaisesRegex(SystemExit, "already used"):
            self.invoke(
                extra=("--expected-client-id", CLIENT, "--expires", EXPIRY_TEXT),
                handler=self.existing_handler(credentials=[]),
            )

    def test_wrong_terraform_binding_rejected(self):
        altered = resources()
        altered["photos_container_id"] = altered["backups_container_id"]
        with mock.patch.object(IDENTITY, "json_command", return_value=altered):
            with self.assertRaisesRegex(SystemExit, "expected Sideyard staging storage"):
                IDENTITY.terraform_resources(ROOT, self.tf)

    def test_unsafe_directory_and_file_are_rejected(self):
        folder = self.home / ".config" / "sideyard-azure"
        folder.mkdir(parents=True)
        folder.chmod(0o755)
        with self.assertRaisesRegex(SystemExit, "directory must be owner-only"):
            self.invoke(
                extra=("--expires", EXPIRY_TEXT), handler=self.creation_handler()
            )
        folder.chmod(0o700)
        target, _ = self.create_file()
        target.chmod(0o640)
        with self.assertRaisesRegex(SystemExit, "file must be owner-only"):
            self.invoke(handler=self.existing_handler())

    def test_rotation_keeps_previous_and_reports_stale_key(self):
        self.create_file(key=OLD_KEY, secret="OLD-SECRET")
        calls = {"lists": 0}

        def handler(command, **_):
            joined = " ".join(command)
            if "account show" in joined:
                return {"id": SUBSCRIPTION, "tenantId": TENANT}
            if "app list" in joined:
                return [app()]
            if "sp list" in joined:
                return [principal()]
            if "credential list" in joined:
                calls["lists"] += 1
                return [credential(OLD_KEY)] if calls["lists"] == 1 else [credential(OLD_KEY), credential(KEY)]
            if "credential reset" in joined:
                return {"password": "NEW-SECRET"}
            self.fail(f"unexpected command: {command}")

        output, _, _, _ = self.invoke(
            extra=("--rotate", "--expected-client-id", CLIENT, "--expires", EXPIRY_TEXT),
            handler=handler,
        )
        result = json.loads(output)
        self.assertEqual(result["credential_key_id"], KEY)
        self.assertEqual(result["stale_key_ids"], [OLD_KEY])
        previous = Path(result["credential_file"] + ".previous")
        self.assertEqual(IDENTITY.read_credential_file(previous)["AZURE_CREDENTIAL_KEY_ID"], OLD_KEY)

    def test_near_expiry_current_credential_can_be_rotated(self):
        near_expiry = (datetime.now(timezone.utc) + timedelta(days=10)).replace(microsecond=0).isoformat().replace("+00:00", "Z")
        target, values = self.create_file(key=OLD_KEY, secret="OLD-SECRET")
        values["AZURE_CREDENTIAL_EXPIRES"] = near_expiry
        target.write_text("".join(f"{name}={value}\n" for name, value in values.items()))
        calls = {"lists": 0}

        def handler(command, **_):
            joined = " ".join(command)
            if "account show" in joined:
                return {"id": SUBSCRIPTION, "tenantId": TENANT}
            if "app list" in joined:
                return [app()]
            if "sp list" in joined:
                return [principal()]
            if "credential list" in joined:
                calls["lists"] += 1
                old = credential(OLD_KEY, expiry=near_expiry)
                return [old] if calls["lists"] == 1 else [old, credential(KEY)]
            if "credential reset" in joined:
                return {"password": "NEW-SECRET"}
            self.fail(f"unexpected command: {command}")

        output, _, _, _ = self.invoke(
            extra=("--rotate", "--expected-client-id", CLIENT, "--expires", EXPIRY_TEXT),
            handler=handler,
        )
        self.assertEqual(json.loads(output)["credential_key_id"], KEY)

    def test_expired_current_credential_can_be_rotated(self):
        expired = (datetime.now(timezone.utc) - timedelta(days=1)).replace(microsecond=0).isoformat().replace("+00:00", "Z")
        target, values = self.create_file(key=OLD_KEY, secret="OLD-SECRET")
        values["AZURE_CREDENTIAL_EXPIRES"] = expired
        target.write_text("".join(f"{name}={value}\n" for name, value in values.items()))
        calls = {"lists": 0}

        def handler(command, **_):
            joined = " ".join(command)
            if "account show" in joined:
                return {"id": SUBSCRIPTION, "tenantId": TENANT}
            if "app list" in joined:
                return [app()]
            if "sp list" in joined:
                return [principal()]
            if "credential list" in joined:
                calls["lists"] += 1
                old = credential(OLD_KEY, expiry=expired)
                return [old] if calls["lists"] == 1 else [old, credential(KEY)]
            if "credential reset" in joined:
                return {"password": "NEW-SECRET"}
            self.fail(f"unexpected command: {command}")

        output, _, _, _ = self.invoke(
            extra=("--rotate", "--expected-client-id", CLIENT, "--expires", EXPIRY_TEXT),
            handler=handler,
        )
        self.assertEqual(json.loads(output)["credential_key_id"], KEY)

    def test_current_key_cannot_be_revoked(self):
        self.create_file()
        with self.assertRaisesRegex(SystemExit, "current protected"):
            self.invoke(
                extra=("--revoke-key-id", KEY), handler=self.existing_handler()
            )

    def test_rollback_restores_prior_key_and_revokes_replacement(self):
        target, current_values = self.create_file(key=KEY, secret="NEW-SECRET")
        previous_values = dict(current_values)
        previous_values["AZURE_CLIENT_SECRET"] = "OLD-SECRET"
        previous_values["AZURE_CREDENTIAL_KEY_ID"] = OLD_KEY
        previous = target.with_name(target.name + ".previous")
        previous.write_text("".join(f"{name}={value}\n" for name, value in previous_values.items()))
        previous.chmod(0o600)
        output, _, _, run_mock = self.invoke(
            extra=("--rollback-rotation",),
            handler=self.existing_handler(credentials=[credential(OLD_KEY), credential(KEY)]),
        )
        result = json.loads(output)
        self.assertEqual(result["restored_key_id"], OLD_KEY)
        self.assertEqual(result["revoked_replacement_key_id"], KEY)
        self.assertEqual(IDENTITY.read_credential_file(target)["AZURE_CREDENTIAL_KEY_ID"], OLD_KEY)
        self.assertFalse(previous.exists())
        self.assertIn(KEY, " ".join(str(call) for call in run_mock.call_args_list))

    def test_interrupted_rollback_resumes_from_durable_journal(self):
        target, current_values = self.create_file(key=KEY, secret="NEW-SECRET")
        previous_values = dict(current_values)
        previous_values["AZURE_CLIENT_SECRET"] = "OLD-SECRET"
        previous_values["AZURE_CREDENTIAL_KEY_ID"] = OLD_KEY
        previous = target.with_name(target.name + ".previous")
        previous.write_text("".join(f"{name}={value}\n" for name, value in previous_values.items()))
        previous.chmod(0o600)
        real_write = IDENTITY.write_credential_file
        rollback_journal = target.with_name(target.name + ".rollback")

        def stop_after_journal(path, values):
            result = real_write(path, values)
            if path == rollback_journal:
                raise KeyboardInterrupt("simulated hard stop")
            return result

        with mock.patch.object(IDENTITY, "write_credential_file", side_effect=stop_after_journal):
            with self.assertRaises(KeyboardInterrupt):
                self.invoke(
                    extra=("--rollback-rotation",),
                    handler=self.existing_handler(credentials=[credential(OLD_KEY), credential(KEY)]),
                )
        self.assertTrue(rollback_journal.exists())
        self.assertEqual(IDENTITY.read_credential_file(target)["AZURE_CREDENTIAL_KEY_ID"], KEY)
        self.assertEqual(IDENTITY.read_credential_file(previous)["AZURE_CREDENTIAL_KEY_ID"], OLD_KEY)
        output, _, _, _ = self.invoke(
            extra=("--rollback-rotation",),
            handler=self.existing_handler(credentials=[credential(OLD_KEY), credential(KEY)]),
        )
        self.assertEqual(json.loads(output)["restored_key_id"], OLD_KEY)
        self.assertFalse(previous.exists())
        self.assertFalse(rollback_journal.exists())
        output, _, _, _ = self.invoke(handler=self.existing_handler(credentials=[credential(OLD_KEY)]))
        self.assertEqual(json.loads(output)["credential_key_id"], OLD_KEY)

    def test_explicit_old_key_revoke_preserves_current_file(self):
        target, values = self.create_file()
        prior_values = dict(values)
        prior_values["AZURE_CLIENT_SECRET"] = "OLD-SECRET"
        prior_values["AZURE_CREDENTIAL_KEY_ID"] = OLD_KEY
        previous = target.with_name(target.name + ".previous")
        previous.write_text("".join(f"{name}={value}\n" for name, value in prior_values.items()))
        previous.chmod(0o600)
        commands = []

        def run_handler(command, **_):
            commands.append(" ".join(command))
            return mock.Mock(stdout="", returncode=0)

        output, _, _, _ = self.invoke(
            extra=("--revoke-key-id", OLD_KEY),
            handler=self.existing_handler(credentials=[credential(OLD_KEY), credential(KEY)]),
            run_handler=run_handler,
        )
        self.assertEqual(json.loads(output)["revoked_key_id"], OLD_KEY)
        self.assertEqual(IDENTITY.read_credential_file(target)["AZURE_CREDENTIAL_KEY_ID"], KEY)
        self.assertTrue(any("credential delete" in command and OLD_KEY in command for command in commands))

    def test_expired_saved_key_can_be_revoked(self):
        expired = (datetime.now(timezone.utc) - timedelta(days=1)).replace(microsecond=0).isoformat().replace("+00:00", "Z")
        target, values = self.create_file()
        prior_values = dict(values)
        prior_values["AZURE_CLIENT_SECRET"] = "OLD-SECRET"
        prior_values["AZURE_CREDENTIAL_KEY_ID"] = OLD_KEY
        prior_values["AZURE_CREDENTIAL_EXPIRES"] = expired
        previous = target.with_name(target.name + ".previous")
        previous.write_text("".join(f"{name}={value}\n" for name, value in prior_values.items()))
        previous.chmod(0o600)
        output, _, _, _ = self.invoke(
            extra=("--revoke-key-id", OLD_KEY),
            handler=self.existing_handler(credentials=[credential(OLD_KEY, expiry=expired), credential(KEY)]),
        )
        self.assertEqual(json.loads(output)["revoked_key_id"], OLD_KEY)

    def test_revoke_refuses_to_leave_only_an_expired_current_key(self):
        expired = (datetime.now(timezone.utc) - timedelta(days=1)).replace(microsecond=0).isoformat().replace("+00:00", "Z")
        target, current_values = self.create_file(key=KEY, secret="EXPIRED-CURRENT")
        current_values["AZURE_CREDENTIAL_EXPIRES"] = expired
        target.write_text("".join(f"{name}={value}\n" for name, value in current_values.items()))
        prior_values = dict(current_values)
        prior_values["AZURE_CLIENT_SECRET"] = "LIVE-PRIOR"
        prior_values["AZURE_CREDENTIAL_KEY_ID"] = OLD_KEY
        prior_values["AZURE_CREDENTIAL_EXPIRES"] = EXPIRY_TEXT
        previous = target.with_name(target.name + ".previous")
        previous.write_text("".join(f"{name}={value}\n" for name, value in prior_values.items()))
        previous.chmod(0o600)
        with self.assertRaisesRegex(SystemExit, "has expired"):
            self.invoke(
                extra=("--revoke-key-id", OLD_KEY),
                handler=self.existing_handler(credentials=[credential(OLD_KEY), credential(KEY, expiry=expired)]),
            )

    def test_expired_rollback_target_fails_before_journal_creation(self):
        expired = (datetime.now(timezone.utc) - timedelta(days=1)).replace(microsecond=0).isoformat().replace("+00:00", "Z")
        target, current_values = self.create_file(key=KEY, secret="NEW-SECRET")
        prior_values = dict(current_values)
        prior_values["AZURE_CLIENT_SECRET"] = "EXPIRED-PRIOR"
        prior_values["AZURE_CREDENTIAL_KEY_ID"] = OLD_KEY
        prior_values["AZURE_CREDENTIAL_EXPIRES"] = expired
        previous = target.with_name(target.name + ".previous")
        previous.write_text("".join(f"{name}={value}\n" for name, value in prior_values.items()))
        previous.chmod(0o600)
        with self.assertRaisesRegex(SystemExit, "has expired"):
            self.invoke(
                extra=("--rollback-rotation",),
                handler=self.existing_handler(credentials=[credential(OLD_KEY, expiry=expired), credential(KEY)]),
            )
        self.assertFalse(target.with_name(target.name + ".rollback").exists())

    def test_expired_replacement_can_be_rolled_back_to_live_prior_key(self):
        expired = (datetime.now(timezone.utc) - timedelta(days=1)).replace(microsecond=0).isoformat().replace("+00:00", "Z")
        target, current_values = self.create_file(key=KEY, secret="NEW-SECRET")
        current_values["AZURE_CREDENTIAL_EXPIRES"] = expired
        target.write_text("".join(f"{name}={value}\n" for name, value in current_values.items()))
        prior_values = dict(current_values)
        prior_values["AZURE_CLIENT_SECRET"] = "OLD-SECRET"
        prior_values["AZURE_CREDENTIAL_KEY_ID"] = OLD_KEY
        prior_values["AZURE_CREDENTIAL_EXPIRES"] = EXPIRY_TEXT
        previous = target.with_name(target.name + ".previous")
        previous.write_text("".join(f"{name}={value}\n" for name, value in prior_values.items()))
        previous.chmod(0o600)
        output, _, _, _ = self.invoke(
            extra=("--rollback-rotation",),
            handler=self.existing_handler(credentials=[credential(OLD_KEY), credential(KEY, expiry=expired)]),
        )
        self.assertEqual(json.loads(output)["restored_key_id"], OLD_KEY)

    def test_every_completed_rollback_cleanup_state_is_resumable(self):
        target, old_values = self.create_file(key=OLD_KEY, secret="OLD-SECRET")
        failed_values = dict(old_values)
        failed_values["AZURE_CLIENT_SECRET"] = "FAILED-NEW"
        failed_values["AZURE_CREDENTIAL_KEY_ID"] = KEY
        previous = target.with_name(target.name + ".previous")
        rollback = target.with_name(target.name + ".rollback")

        # Cloud revocation completed; both cleanup files remain.
        previous.write_text("".join(f"{name}={value}\n" for name, value in old_values.items()))
        previous.chmod(0o600)
        rollback.write_text("".join(f"{name}={value}\n" for name, value in failed_values.items()))
        rollback.chmod(0o600)
        output, _, _, _ = self.invoke(
            extra=("--rollback-rotation",),
            handler=self.existing_handler(credentials=[credential(OLD_KEY)]),
        )
        self.assertEqual(json.loads(output)["restored_key_id"], OLD_KEY)

        # `.previous` cleanup completed; `.rollback` remains.
        rollback.write_text("".join(f"{name}={value}\n" for name, value in failed_values.items()))
        rollback.chmod(0o600)
        output, _, _, _ = self.invoke(
            extra=("--rollback-rotation",),
            handler=self.existing_handler(credentials=[credential(OLD_KEY)]),
        )
        self.assertEqual(json.loads(output)["restored_key_id"], OLD_KEY)

        # Both cleanup files are gone; retry is an idempotent acknowledgement.
        output, _, _, _ = self.invoke(
            extra=("--rollback-rotation",),
            handler=self.existing_handler(credentials=[credential(OLD_KEY)]),
        )
        self.assertEqual(json.loads(output)["rollback_status"], "already_complete")

    def test_persistence_failure_deletes_new_cloud_key(self):
        commands = []

        def run_handler(command, **_):
            commands.append(command)
            return mock.Mock(stdout="", returncode=0)

        with mock.patch.object(IDENTITY, "write_credential_file", side_effect=OSError("disk full")):
            with self.assertRaisesRegex(OSError, "disk full"):
                self.invoke(
                    extra=("--expires", EXPIRY_TEXT),
                    handler=self.creation_handler(),
                    run_handler=run_handler,
                )
        flattened = [" ".join(command) for command in commands]
        self.assertTrue(any("credential delete" in command and KEY in command for command in flattened))

    def test_rotation_double_filesystem_failure_still_deletes_new_cloud_key(self):
        self.create_file(key=OLD_KEY, secret="OLD-SECRET")
        calls = {"lists": 0}

        def handler(command, **_):
            joined = " ".join(command)
            if "account show" in joined:
                return {"id": SUBSCRIPTION, "tenantId": TENANT}
            if "app list" in joined:
                return [app()]
            if "sp list" in joined:
                return [principal()]
            if "credential list" in joined:
                calls["lists"] += 1
                return [credential(OLD_KEY)] if calls["lists"] == 1 else [credential(OLD_KEY), credential(KEY)]
            if "credential reset" in joined:
                return {"password": "NEW-SECRET"}
            self.fail(f"unexpected command: {command}")

        commands = []

        def run_handler(command, **_):
            commands.append(" ".join(command))
            return mock.Mock(stdout="", returncode=0)

        with mock.patch.object(IDENTITY, "write_credential_file", side_effect=OSError("disk full")):
            with self.assertRaisesRegex(SystemExit, "protected local credential requires repair"):
                self.invoke(
                    extra=("--rotate", "--expected-client-id", CLIENT, "--expires", EXPIRY_TEXT),
                    handler=handler,
                    run_handler=run_handler,
                )
        self.assertTrue(any("credential delete" in command and KEY in command for command in commands))

    def test_expiry_must_exceed_thirty_days(self):
        short = (datetime.now(timezone.utc) + timedelta(days=30)).isoformat()
        with self.assertRaises(SystemExit):
            self.invoke(extra=("--expires", short), handler=self.creation_handler())

    def test_terraform_roles_have_exact_actions_scopes_and_separation(self):
        main = (ROOT / "infra/azure/environments/main.tf").read_text()
        variables = (ROOT / "infra/azure/environments/variables.tf").read_text()
        publisher = main.split('resource "azurerm_role_definition" "logical_backup_publisher"', 1)[1].split('resource "azurerm_role_assignment" "logical_backup_publisher"', 1)[0]
        reader = main.split('resource "azurerm_role_definition" "logical_photo_reader"', 1)[1].split('resource "azurerm_role_assignment" "logical_photo_reader"', 1)[0]
        publisher_assignment = main.split('resource "azurerm_role_assignment" "logical_backup_publisher"', 1)[1].split('resource "azurerm_role_definition" "logical_photo_reader"', 1)[0]
        reader_assignment = main.split('resource "azurerm_role_assignment" "logical_photo_reader"', 1)[1].split('# Export keys', 1)[0]
        self.assertEqual(publisher.count('containers/blobs/read"'), 1)
        self.assertEqual(publisher.count('containers/blobs/write"'), 1)
        self.assertNotIn("delete", publisher.lower())
        self.assertEqual(reader.count('containers/blobs/read"'), 1)
        reader_actions = reader.split("data_actions = [", 1)[1].split("]", 1)[0]
        self.assertNotIn("/write", reader_actions.lower())
        self.assertNotIn("/delete", reader_actions.lower())
        self.assertIn("/containers/backups", publisher_assignment)
        self.assertNotIn("/containers/photos", publisher_assignment)
        self.assertIn("/containers/photos", reader_assignment)
        self.assertNotIn("/containers/backups", reader_assignment)
        self.assertIn('var.environment == "staging"', main)
        self.assertIn("setintersection", main)
        self.assertIn("lower(var.operator_object_id)", main)
        self.assertIn("for id in var.runtime_object_ids : lower(id)", main)
        self.assertIn("for id in var.logical_backup_publisher_object_ids : lower(id)", main)
        self.assertIn('for_each    = var.environment == "staging"', publisher)
        self.assertIn('for_each    = var.environment == "staging"', reader)


if __name__ == "__main__":
    unittest.main()
