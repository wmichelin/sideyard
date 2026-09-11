#!/usr/bin/env python3
"""Focused tests for certificate-only recovery identity binding."""

from contextlib import redirect_stdout
from datetime import datetime, timedelta, timezone
import base64
import importlib.util
import io
import json
import subprocess
from pathlib import Path
import stat
import sys
import tempfile
import unittest
from unittest import mock


SOURCE = Path(__file__).with_name("verify-recovery-certificate.py")
SPEC = importlib.util.spec_from_file_location("verify_recovery_certificate", SOURCE)
VERIFY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(VERIFY)

SUBSCRIPTION = "11111111-1111-1111-1111-111111111111"
TENANT = "22222222-2222-2222-2222-222222222222"
CLIENT = "33333333-3333-3333-3333-333333333333"
PRINCIPAL = "44444444-4444-4444-4444-444444444444"
KEY_ID = "55555555-5555-5555-5555-555555555555"
THUMBPRINT = "00112233445566778899AABBCCDDEEFF00112233"
ROLE_ID = "66666666-6666-6666-6666-666666666666"
RUNNER = "77777777-7777-7777-7777-777777777777"
EXPIRY = (datetime.now(timezone.utc) + timedelta(days=90)).replace(microsecond=0)
ACCOUNT = "sideyardtestaccount"
ACCOUNT_ID = (
    f"/subscriptions/{SUBSCRIPTION}/resourceGroups/sideyard-staging/"
    f"providers/Microsoft.Storage/storageAccounts/{ACCOUNT}"
)


def resources(purpose="logical-backup-publisher"):
    return {
        "environment": "staging",
        "resource_group": "sideyard-staging",
        "storage_account": ACCOUNT,
        "storage_account_id": ACCOUNT_ID,
        "blob_endpoint": f"https://{ACCOUNT}.blob.core.windows.net/",
        "terraform_runner_object_id": RUNNER,
        "backups_container_id": f"{ACCOUNT_ID}/blobServices/default/containers/backups",
        "photos_container_id": f"{ACCOUNT_ID}/blobServices/default/containers/photos",
        "logical_backup_publisher_role_definition_id": (
            f"/subscriptions/{SUBSCRIPTION}/providers/Microsoft.Authorization/roleDefinitions/{ROLE_ID}"
        ),
        "logical_photo_reader_role_definition_id": (
            f"/subscriptions/{SUBSCRIPTION}/providers/Microsoft.Authorization/roleDefinitions/{ROLE_ID}"
        ),
    }


def definition(purpose):
    actions = ["Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read"]
    if purpose == "logical-backup-publisher":
        actions.append("Microsoft.Storage/storageAccounts/blobServices/containers/blobs/write")
    return {
        "id": resources()[f"{purpose.replace('-', '_')}_role_definition_id"],
        "assignableScopes": [f"/subscriptions/{SUBSCRIPTION}/resourceGroups/sideyard-staging"],
        "permissions": [{
            "actions": [],
            "notActions": [],
            "dataActions": actions,
            "notDataActions": [],
        }],
    }


class RecoveryCertificateTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.home = Path(self.temp.name)
        self.folder = self.home / ".config" / "sideyard-azure"
        self.folder.mkdir(parents=True, mode=0o700)
        self.tf = self.home / "tf"
        (self.tf / ".terraform" / "staging").mkdir(parents=True)
        (self.tf / "staging.hcl").write_text("key = \"staging.tfstate\"\n")

    def tearDown(self):
        self.temp.cleanup()

    def invoke(self, purpose="logical-backup-publisher", *, assigned=False, require_assigned=None, extra=(), resource_values=None):
        if require_assigned is None:
            require_assigned = assigned
        if resource_values is None:
            resource_values = resources()
        argv = [
            str(SOURCE), "staging", purpose,
            "--terraform-directory", str(self.tf),
            "--require-assigned" if require_assigned else "--require-unassigned",
            *extra,
        ]
        key = self.folder / f"staging-{purpose}-key.pem"
        cert = self.folder / f"staging-{purpose}-cert.pem"
        combined = self.folder / f"staging-{purpose}.pem"
        for path, content in ((key, "key"), (cert, "cert"), (combined, "certkey")):
            path.write_text(content)
            path.chmod(0o600)

        role_id = resource_values[f"{purpose.replace('-', '_')}_role_definition_id"]
        container = VERIFY.PURPOSES[purpose]
        assignments = [{
            "principalId": PRINCIPAL,
            "scope": resource_values[f"{container}_container_id"],
            "roleDefinitionId": role_id,
        }] if assigned else []

        commands = []

        def command(command, **_):
            commands.append(command)
            joined = " ".join(command)
            if "account show" in joined:
                return {"id": SUBSCRIPTION, "tenantId": TENANT}
            if "account list" in joined:
                return [{"id": SUBSCRIPTION, "state": "Enabled"}]
            if "role assignment list" in joined:
                return assignments
            if "role definition list" in joined:
                return [definition(purpose)]
            self.fail(f"unexpected command: {command}")

        output = io.StringIO()
        with (
            mock.patch.object(sys, "argv", argv),
            mock.patch.object(VERIFY.Path, "home", return_value=self.home),
            mock.patch.object(VERIFY, "terraform_resources", return_value=(resource_values, SUBSCRIPTION, ACCOUNT)),
            mock.patch.object(VERIFY, "certificate_metadata", return_value=(self.folder, key, cert, combined, "ABCD", THUMBPRINT, EXPIRY)),
            mock.patch.object(VERIFY, "json_command", side_effect=command),
            mock.patch.object(VERIFY, "authenticate_certificate") as authenticate,
            redirect_stdout(output),
        ):
            VERIFY.main()
        return json.loads(output.getvalue()), authenticate, commands

    def binding_args(self):
        return (
            "--client-id", CLIENT,
            "--principal-object-id", PRINCIPAL,
            "--credential-key-id", KEY_ID,
            "--credential-expires", EXPIRY.isoformat().replace("+00:00", "Z"),
            "--credential-thumbprint", THUMBPRINT,
        )

    def test_initial_binding_is_owner_only_and_authenticates(self):
        result, authenticate, commands = self.invoke(extra=self.binding_args())
        target = Path(result["credential_file"])
        self.assertEqual(stat.S_IMODE(target.stat().st_mode), 0o600)
        self.assertEqual(result["assignment_status"], "unassigned")
        self.assertEqual(result["certificate_sha256"], "ABCD")
        authenticate.assert_called_once()
        self.assertNotIn("SECRET", target.read_text())
        role_commands = [command for command in commands if command[:4] == ["az", "role", "definition", "list"]]
        self.assertEqual(len(role_commands), 1)
        self.assertEqual(role_commands[0][role_commands[0].index("--name") + 1], ROLE_ID)
        assignment_commands = [command for command in commands if command[:4] == ["az", "role", "assignment", "list"]]
        self.assertEqual(len(assignment_commands), 2)
        self.assertTrue(any("--all" in command for command in assignment_commands))
        self.assertTrue(any("--scope" in command for command in assignment_commands))
        self.assertTrue(all(command[command.index("--fill-principal-name") + 1] == "false" for command in assignment_commands))

    def test_assigned_identity_requires_exact_container_role(self):
        self.invoke(extra=self.binding_args())
        result, _, _ = self.invoke(assigned=True)
        self.assertEqual(result["assignment_status"], "assigned")

    def test_existing_binding_rejects_identifier_drift(self):
        self.invoke(extra=self.binding_args())
        wrong = "77777777-7777-7777-7777-777777777777"
        with self.assertRaisesRegex(SystemExit, "do not match"):
            self.invoke(extra=(
                "--client-id", wrong,
                "--principal-object-id", PRINCIPAL,
                "--credential-key-id", KEY_ID,
                "--credential-expires", EXPIRY.isoformat().replace("+00:00", "Z"),
                "--credential-thumbprint", THUMBPRINT,
            ))

    def test_wrong_assignment_gate_fails(self):
        with self.assertRaisesRegex(SystemExit, "must be assigned"):
            self.invoke(assigned=False, require_assigned=True, extra=self.binding_args())

    def test_recovery_principal_cannot_reuse_terraform_runner(self):
        changed = resources()
        changed["terraform_runner_object_id"] = PRINCIPAL
        with self.assertRaisesRegex(SystemExit, "must not reuse"):
            self.invoke(extra=self.binding_args(), resource_values=changed)

    def test_certificate_expiry_must_match_cloud_metadata(self):
        wrong_expiry = (EXPIRY + timedelta(days=1)).isoformat().replace("+00:00", "Z")
        with self.assertRaisesRegex(SystemExit, "does not match"):
            self.invoke(extra=(
                "--client-id", CLIENT,
                "--principal-object-id", PRINCIPAL,
                "--credential-key-id", KEY_ID,
                "--credential-expires", wrong_expiry,
                "--credential-thumbprint", THUMBPRINT,
            ))

    def access_token(self, **changes):
        now = datetime.now(timezone.utc).timestamp()
        payload = {
            "azp": CLIENT,
            "oid": PRINCIPAL,
            "tid": TENANT,
            "aud": "https://storage.azure.com/",
            "nbf": now - 10,
            "exp": now + 3600,
        }
        payload.update(changes)
        encoded = base64.urlsafe_b64encode(json.dumps(payload).encode()).decode().rstrip("=")
        return {"accessToken": f"header.{encoded}.signature", "tokenType": "Bearer", "expires_on": int(now + 3600)}

    def test_certificate_auth_binds_client_principal_tenant_and_audience(self):
        values = {
            "AZURE_CLIENT_ID": CLIENT,
            "AZURE_PRINCIPAL_OBJECT_ID": PRINCIPAL,
            "AZURE_TENANT_ID": TENANT,
            "AZURE_CLIENT_CERTIFICATE_PATH": str(self.folder / "combined.pem"),
        }
        Path(values["AZURE_CLIENT_CERTIFICATE_PATH"]).write_text("certificate and private key")
        Path(values["AZURE_CLIENT_CERTIFICATE_PATH"]).chmod(0o600)
        access_token = self.access_token()
        with (
            mock.patch.object(VERIFY.shutil, "which", return_value="/test/az"),
            mock.patch.object(VERIFY, "run") as runner,
            mock.patch.object(VERIFY, "json_command", return_value=access_token),
        ):
            token = VERIFY.authenticate_certificate(self.home, self.folder, values)
        self.assertEqual(token, access_token["accessToken"])
        command = runner.call_args.args[0]
        self.assertIn("--service-principal", command)
        self.assertEqual(command[command.index("--username") + 1], CLIENT)
        self.assertEqual(Path(command[command.index("--certificate") + 1]).name, "identity.pem")
        self.assertNotEqual(command[command.index("--certificate") + 1], values["AZURE_CLIENT_CERTIFICATE_PATH"])

        with (
            mock.patch.object(VERIFY.shutil, "which", return_value="/test/az"),
            mock.patch.object(VERIFY, "run"),
            mock.patch.object(VERIFY, "json_command", return_value=self.access_token(oid=KEY_ID)),
            self.assertRaisesRegex(SystemExit, "bounded Blob access token"),
        ):
            VERIFY.authenticate_certificate(self.home, self.folder, values)
        with (
            mock.patch.object(VERIFY.shutil, "which", return_value="/test/az"),
            mock.patch.object(VERIFY, "run"),
            mock.patch.object(VERIFY, "json_command", return_value=self.access_token(exp=datetime.now(timezone.utc).timestamp()+300)),
            self.assertRaisesRegex(SystemExit, "bounded Blob access token"),
        ):
            VERIFY.authenticate_certificate(self.home, self.folder, values)

    def test_existing_identity_is_revalidated_and_returns_token(self):
        purpose="logical-backup-publisher"
        key=self.folder/f"staging-{purpose}-key.pem"; cert=self.folder/f"staging-{purpose}-cert.pem"; combined=self.folder/f"staging-{purpose}.pem"
        for path in (key,cert,combined): path.write_text("protected"); path.chmod(0o600)
        values=VERIFY.expected_values(
            purpose=purpose,resources=resources(),subscription=SUBSCRIPTION,tenant=TENANT,
            client_id=CLIENT,principal_id=PRINCIPAL,key_id=KEY_ID,fingerprint="ABCD",
            azure_thumbprint=THUMBPRINT,expiry=EXPIRY,key=key,cert=cert,combined=combined,
        )
        VERIFY.write_values(self.folder/f"staging-{purpose}.env",values)
        with (
            mock.patch.object(VERIFY.Path,"home",return_value=self.home),
            mock.patch.object(VERIFY,"certificate_metadata",return_value=(self.folder,key,cert,combined,"ABCD",THUMBPRINT,EXPIRY)),
            mock.patch.object(VERIFY,"audit_rbac") as audit,
            mock.patch.object(VERIFY,"authenticate_certificate",return_value="bounded-token") as authenticate,
        ):
            bound,token=VERIFY.authenticate_existing_identity(self.home,resources(),SUBSCRIPTION,TENANT,purpose)
        self.assertEqual(bound,values); self.assertEqual(token,"bounded-token")
        audit.assert_called_once_with(self.home,resources(),SUBSCRIPTION,purpose,PRINCIPAL,True,environment=None)
        authenticate.assert_called_once_with(self.home,self.folder,values,None)

    def test_subprocess_failure_does_not_repeat_sensitive_stderr(self):
        failure = subprocess.CalledProcessError(1, ["az"], stderr="SENTINEL-SECRET")
        with mock.patch.object(VERIFY.subprocess, "run", side_effect=failure):
            with self.assertRaises(SystemExit) as caught:
                VERIFY.run(["az"], cwd=self.home)
        self.assertNotIn("SENTINEL-SECRET", str(caught.exception))

    def test_retry_removes_safe_stale_auth_profile(self):
        stale = self.folder / ".recovery-auth-interrupted"; runner = self.folder / ".terraform-runner-interrupted"
        for candidate in (stale,runner):
            candidate.mkdir(mode=0o700); token=candidate / "token-cache"; token.write_text("SENTINEL-TOKEN"); token.chmod(0o600)
        result, _, _ = self.invoke(extra=self.binding_args())
        self.assertEqual(result["assignment_status"], "unassigned")
        self.assertFalse(stale.exists())
        self.assertFalse(runner.exists())
        lock = self.folder / ".recovery-certificate.lock"
        self.assertEqual(stat.S_IMODE(lock.stat().st_mode), 0o600)

    def test_retry_refuses_unsafe_stale_auth_profile(self):
        outside = self.home / "outside"
        outside.mkdir()
        stale = self.folder / ".recovery-auth-interrupted"
        stale.symlink_to(outside, target_is_directory=True)
        with self.assertRaisesRegex(SystemExit, "unsafe"):
            self.invoke(extra=self.binding_args())

    def test_retry_refuses_symlink_inside_stale_auth_profile(self):
        stale = self.folder / ".terraform-runner-interrupted"
        stale.mkdir(mode=0o700)
        outside = self.home / "outside-token"
        outside.write_text("do not remove")
        (stale / "token-cache").symlink_to(outside)
        with self.assertRaisesRegex(SystemExit, "unsafe"):
            self.invoke(extra=self.binding_args())
        self.assertEqual(outside.read_text(), "do not remove")


if __name__ == "__main__":
    unittest.main()
