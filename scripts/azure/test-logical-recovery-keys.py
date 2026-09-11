#!/usr/bin/env python3
"""Focused tests for logical-recovery key preparation and custody gates."""

import importlib.util
import io
import json
import os
from pathlib import Path
import signal
import subprocess
import tempfile
import unittest
from unittest import mock


ROOT = Path(__file__).resolve().parents[2]
SOURCE = ROOT / "scripts" / "azure" / "logical-recovery-keys.py"
SPEC = importlib.util.spec_from_file_location("sideyard_test_logical_recovery_keys", SOURCE)
KEYS = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(KEYS)
REVISION = "a" * 40


class LogicalRecoveryKeyTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.config = self.root / "config"
        self.config.mkdir(mode=0o700)
        self.key_file = self.config / "keys.env"
        self.prepared = self.config / "prepared.json"
        self.copy_a = self.config / "staging-logical-recovery-copy-a.json"
        self.copy_b = self.config / "staging-logical-recovery-copy-b.json"
        self.attestation = self.config / "staging-logical-recovery-custody.json"

    def initialize(self):
        with (
            mock.patch.object(KEYS, "reviewed_revision", return_value=REVISION),
            mock.patch.object(KEYS.secrets, "token_hex", side_effect=("1" * 64, "2" * 64)),
        ):
            return KEYS.initialize(self.key_file, self.prepared)

    def retrieved(self, name):
        folder = self.root / name
        folder.mkdir(mode=0o700)
        target = folder / "retrieved.env"
        target.write_bytes(self.key_file.read_bytes())
        target.chmod(0o600)
        return target

    def verify(self, slot, label, receipt, delete=False):
        target = self.retrieved("retrieved-" + slot)
        evidence = KEYS.expected_synthetic_digests()
        with (
            mock.patch.object(KEYS, "reviewed_revision", return_value=REVISION),
            mock.patch.object(KEYS, "synthetic_recovery", return_value=evidence),
        ):
            result = KEYS.verify_copy(
                self.key_file, self.prepared, target, receipt, slot, label, delete
            )
        self.assertEqual(target.exists(), not delete)
        return result, target

    def test_prepares_distinct_keys_once_without_exposing_them(self):
        prepared = self.initialize()
        values, raw = KEYS.read_keys(self.key_file)
        self.assertEqual(set(values), set(KEYS.KEY_NAMES))
        self.assertNotEqual(values[KEYS.KEY_NAMES[0]], values[KEYS.KEY_NAMES[1]])
        self.assertEqual(self.key_file.stat().st_mode & 0o777, 0o600)
        self.assertEqual(self.prepared.stat().st_mode & 0o777, 0o600)
        public = json.dumps(KEYS.public_result(prepared))
        self.assertNotIn(values[KEYS.KEY_NAMES[0]], public)
        self.assertNotIn(values[KEYS.KEY_NAMES[1]], public)
        with mock.patch.object(KEYS, "reviewed_revision", return_value=REVISION):
            repeated = KEYS.initialize(self.key_file, self.prepared)
        self.assertEqual(repeated, prepared)
        self.assertEqual(self.key_file.read_bytes(), raw)

    def test_rejects_noncanonical_equal_and_unsafe_key_files(self):
        self.key_file.write_text(
            f"{KEYS.KEY_NAMES[0]}={'1' * 64}\n{KEYS.KEY_NAMES[1]}={'1' * 64}\n"
        )
        self.key_file.chmod(0o600)
        with self.assertRaisesRegex(ValueError, "distinct"):
            KEYS.read_keys(self.key_file)
        self.key_file.write_text(
            f"{KEYS.KEY_NAMES[1]}={'2' * 64}\n{KEYS.KEY_NAMES[0]}={'1' * 64}\n"
        )
        self.key_file.chmod(0o600)
        with self.assertRaisesRegex(ValueError, "canonical"):
            KEYS.read_keys(self.key_file)
        self.key_file.chmod(0o644)
        with self.assertRaisesRegex(ValueError, "private regular"):
            KEYS.read_keys(self.key_file)

    def test_copy_verification_and_owner_attestation_gate(self):
        prepared = self.initialize()
        copy_a,_ = self.verify("a", "manager-copy", self.copy_a)
        copy_b,_ = self.verify("b", "offline-media", self.copy_b)
        self.assertEqual(copy_a["kit_id"], prepared["kit_id"])
        self.assertEqual(copy_b["kit_id"], prepared["kit_id"])
        with self.assertRaisesRegex(ValueError, "explicit confirmation"):
            KEYS.attest(self.key_file, self.prepared, self.copy_a, self.copy_b, self.attestation, False)
        with mock.patch.object(KEYS, "reviewed_revision", return_value=REVISION):
            attested = KEYS.attest(
                self.key_file, self.prepared, self.copy_a, self.copy_b, self.attestation, True
            )
            repeated = KEYS.attest(
                self.key_file, self.prepared, self.copy_a, self.copy_b, self.attestation, True
            )
        self.assertEqual(attested, repeated)
        self.assertEqual(KEYS.verify_active_custody(self.key_file, self.attestation), attested)
        values, _ = KEYS.read_keys(self.key_file)
        values[KEYS.KEY_NAMES[0]] = "5" * 64
        self.key_file.write_text("".join(f"{name}={values[name]}\n" for name in KEYS.KEY_NAMES))
        self.key_file.chmod(0o600)
        with self.assertRaisesRegex(ValueError, "does not match"):
            KEYS.verify_active_custody(self.key_file, self.attestation)

    def test_tampered_attestation_or_copy_evidence_fails(self):
        self.initialize()
        self.verify("a", "manager-copy", self.copy_a)
        self.verify("b", "offline-media", self.copy_b)
        with mock.patch.object(KEYS, "reviewed_revision", return_value=REVISION):
            KEYS.attest(self.key_file, self.prepared, self.copy_a, self.copy_b, self.attestation, True)
        original_attestation = self.attestation.read_bytes()
        changed = json.loads(original_attestation)
        changed["copy_a_label"] = "changed-copy"
        self.attestation.write_bytes(KEYS.canonical(changed)); self.attestation.chmod(0o600)
        with self.assertRaisesRegex(ValueError, "authentication failed"):
            KEYS.verify_active_custody(self.key_file, self.attestation)
        self.attestation.write_bytes(original_attestation); self.attestation.chmod(0o600)
        copy = json.loads(self.copy_a.read_bytes())
        copy["retrieved_at"] = "2026-09-12T00:00:00Z"
        self.copy_a.write_bytes(KEYS.canonical(copy)); self.copy_a.chmod(0o600)
        with self.assertRaisesRegex(ValueError, "receipt authentication"):
            KEYS.verify_active_custody(self.key_file, self.attestation)

    def test_fabricated_copy_receipts_cannot_be_attested(self):
        prepared = self.initialize()
        package_sha, descriptor_sha = KEYS.expected_synthetic_digests()
        for slot,label,target in (
            ("a", "manager-copy", self.copy_a),
            ("b", "offline-media", self.copy_b),
        ):
            document = {
                **prepared,
                "phase": "copy_verified",
                "custody_slot": slot,
                "custody_label": label,
                "retrieved_at": "2026-09-11T00:00:00Z",
                "synthetic_package_sha256": package_sha,
                "synthetic_descriptor_sha256": descriptor_sha,
                "copy_hmac": "0" * 64,
            }
            target.write_bytes(KEYS.canonical(document)); target.chmod(0o600)
        with self.assertRaisesRegex(ValueError, "receipt authentication"):
            KEYS.attest(
                self.key_file, self.prepared, self.copy_a, self.copy_b,
                self.attestation, True
            )
        self.assertFalse(self.attestation.exists())

    def test_duplicate_labels_and_original_file_substitution_fail(self):
        self.initialize()
        with self.assertRaisesRegex(ValueError, "independent"):
            with mock.patch.object(KEYS, "reviewed_revision", return_value=REVISION):
                KEYS.verify_copy(
                    self.key_file, self.prepared, self.key_file, self.copy_a,
                    "a", "manager-copy"
                )
        self.verify("a", "same-label", self.copy_a)
        self.verify("b", "same-label", self.copy_b)
        with self.assertRaisesRegex(ValueError, "distinct independent"):
            KEYS.attest(self.key_file, self.prepared, self.copy_a, self.copy_b, self.attestation, True)

    def test_mismatched_retrieval_fails_and_is_retained_for_investigation(self):
        self.initialize()
        target = self.retrieved("wrong")
        raw = target.read_text().replace("1" * 64, "9" * 64)
        target.write_text(raw)
        target.chmod(0o600)
        with self.assertRaisesRegex(ValueError, "does not match"):
            KEYS.verify_copy(
                self.key_file, self.prepared, target, self.copy_a,
                "a", "manager-copy"
            )
        self.assertTrue(target.exists())
        self.assertFalse(self.copy_a.exists())

    def test_explicit_cleanup_removes_only_the_retrieved_copy(self):
        self.initialize()
        _,target = self.verify("a", "manager-copy", self.copy_a, delete=True)
        self.assertFalse(target.exists())
        self.assertTrue(self.key_file.exists())
        self.assertTrue(self.copy_a.exists())
        repeated = KEYS.verify_copy(
            self.key_file, self.prepared, target, self.copy_a,
            "a", "manager-copy", True
        )
        self.assertEqual(repeated["phase"], "copy_verified")

    def test_cleanup_rejects_replaced_retrieval_path(self):
        self.initialize()
        target = self.retrieved("replace-during-check")
        evidence = KEYS.expected_synthetic_digests()
        def replace_target(_):
            target.unlink()
            target.write_bytes(self.key_file.read_bytes())
            target.chmod(0o600)
            return evidence
        with (
            mock.patch.object(KEYS, "reviewed_revision", return_value=REVISION),
            mock.patch.object(KEYS, "synthetic_recovery", side_effect=replace_target),
        ):
            with self.assertRaisesRegex(ValueError, "changed before cleanup"):
                KEYS.verify_copy(
                    self.key_file, self.prepared, target, self.copy_a,
                    "a", "manager-copy", True
                )
        self.assertTrue(target.exists())
        self.assertTrue(self.copy_a.exists())

    def test_cleanup_never_deletes_a_replacement_after_final_read(self):
        self.initialize()
        target = self.retrieved("replace-after-final-read")
        replacement = b"replacement must survive\n"
        evidence = KEYS.expected_synthetic_digests()
        original_read = KEYS.read_private_bytes

        def replace_original_after_quarantine_read(path, **kwargs):
            result = original_read(path, **kwargs)
            if path.name.endswith(".sideyard-cleanup"):
                target.write_bytes(replacement)
                target.chmod(0o600)
            return result

        with (
            mock.patch.object(KEYS, "reviewed_revision", return_value=REVISION),
            mock.patch.object(KEYS, "synthetic_recovery", return_value=evidence),
            mock.patch.object(KEYS, "read_private_bytes", side_effect=replace_original_after_quarantine_read),
        ):
            KEYS.verify_copy(
                self.key_file, self.prepared, target, self.copy_a,
                "a", "manager-copy", True
            )
        self.assertEqual(target.read_bytes(), replacement)
        self.assertTrue(self.copy_a.exists())
        self.assertFalse(target.with_name(f".{target.name}.sideyard-cleanup").exists())

    def test_cleanup_restores_claimed_file_when_quarantine_validation_fails(self):
        self.initialize()
        target = self.retrieved("quarantine-read-failure")
        original = target.read_bytes()
        evidence = KEYS.expected_synthetic_digests()
        original_read = KEYS.read_private_bytes

        def reject_quarantine(path, **kwargs):
            if path.name.endswith(".sideyard-cleanup"):
                raise ValueError("simulated quarantine read failure")
            return original_read(path, **kwargs)

        with (
            mock.patch.object(KEYS, "reviewed_revision", return_value=REVISION),
            mock.patch.object(KEYS, "synthetic_recovery", return_value=evidence),
            mock.patch.object(KEYS, "read_private_bytes", side_effect=reject_quarantine),
        ):
            with self.assertRaisesRegex(ValueError, "changed before cleanup"):
                KEYS.verify_copy(
                    self.key_file, self.prepared, target, self.copy_a,
                    "a", "manager-copy", True
                )
        self.assertEqual(target.read_bytes(), original)
        self.assertTrue(self.copy_a.exists())
        self.assertFalse(target.with_name(f".{target.name}.sideyard-cleanup").exists())

    def test_hard_kill_after_quarantine_fsync_converges_on_retry(self):
        self.initialize()
        _,target = self.verify("a", "manager-copy", self.copy_a)
        quarantine = target.with_name(f".{target.name}.sideyard-cleanup")
        evidence = KEYS.expected_synthetic_digests()
        original_fsync = KEYS.fsync_directory
        child = os.fork()
        if child == 0:
            def kill_after_claim(folder):
                original_fsync(folder)
                if quarantine.exists() and not target.exists():
                    os.kill(os.getpid(), signal.SIGKILL)
            with (
                mock.patch.object(KEYS, "reviewed_revision", return_value=REVISION),
                mock.patch.object(KEYS, "synthetic_recovery", return_value=evidence),
                mock.patch.object(KEYS, "fsync_directory", side_effect=kill_after_claim),
            ):
                KEYS.verify_copy(
                    self.key_file, self.prepared, target, self.copy_a,
                    "a", "manager-copy", True
                )
            os._exit(1)
        _,status = os.waitpid(child, 0)
        self.assertTrue(os.WIFSIGNALED(status))
        self.assertEqual(os.WTERMSIG(status), signal.SIGKILL)
        self.assertFalse(target.exists())
        self.assertTrue(quarantine.exists())
        with (
            mock.patch.object(KEYS, "reviewed_revision", return_value=REVISION),
            mock.patch.object(KEYS, "synthetic_recovery", return_value=evidence),
        ):
            KEYS.verify_copy(
                self.key_file, self.prepared, target, self.copy_a,
                "a", "manager-copy", True
            )
        self.assertFalse(target.exists())
        self.assertFalse(quarantine.exists())

    def test_hard_kill_after_unlink_converges_without_hidden_copy(self):
        self.initialize()
        _,target = self.verify("a", "manager-copy", self.copy_a)
        quarantine = target.with_name(f".{target.name}.sideyard-cleanup")
        evidence = KEYS.expected_synthetic_digests()
        original_fsync = KEYS.fsync_directory
        child = os.fork()
        if child == 0:
            saw_claim = False
            def kill_after_unlink(folder):
                nonlocal saw_claim
                original_fsync(folder)
                if quarantine.exists() and not target.exists():
                    saw_claim = True
                elif saw_claim and not quarantine.exists() and not target.exists():
                    os.kill(os.getpid(), signal.SIGKILL)
            with (
                mock.patch.object(KEYS, "reviewed_revision", return_value=REVISION),
                mock.patch.object(KEYS, "synthetic_recovery", return_value=evidence),
                mock.patch.object(KEYS, "fsync_directory", side_effect=kill_after_unlink),
            ):
                KEYS.verify_copy(
                    self.key_file, self.prepared, target, self.copy_a,
                    "a", "manager-copy", True
                )
            os._exit(1)
        _,status = os.waitpid(child, 0)
        self.assertTrue(os.WIFSIGNALED(status))
        self.assertEqual(os.WTERMSIG(status), signal.SIGKILL)
        self.assertFalse(target.exists())
        self.assertFalse(quarantine.exists())
        result = KEYS.verify_copy(
            self.key_file, self.prepared, target, self.copy_a,
            "a", "manager-copy", True
        )
        self.assertEqual(result["phase"], "copy_verified")
        self.assertFalse(target.exists())
        self.assertFalse(quarantine.exists())

    def test_reconciliation_retains_ambiguous_or_malformed_quarantine(self):
        self.initialize()
        target = self.retrieved("ambiguous-cleanup")
        quarantine = target.with_name(f".{target.name}.sideyard-cleanup")
        quarantine.write_bytes(target.read_bytes())
        quarantine.chmod(0o600)
        with self.assertRaisesRegex(ValueError, "manual repair"):
            KEYS.verify_copy(
                self.key_file, self.prepared, target, self.copy_a,
                "a", "manager-copy", True
            )
        self.assertTrue(target.exists())
        self.assertTrue(quarantine.exists())
        target.unlink()
        quarantine.write_bytes(b"not a canonical key file\n")
        quarantine.chmod(0o600)
        with self.assertRaisesRegex(ValueError, "invalid"):
            KEYS.verify_copy(
                self.key_file, self.prepared, target, self.copy_a,
                "a", "manager-copy", True
            )
        self.assertFalse(target.exists())
        self.assertTrue(quarantine.exists())

    def test_atomic_claim_and_restore_never_clobber_competing_files(self):
        self.initialize()
        target = self.retrieved("claim-collision")
        original = target.read_bytes()
        quarantine = target.with_name(f".{target.name}.sideyard-cleanup")
        competitor = b"competing quarantine survives\n"
        evidence = KEYS.expected_synthetic_digests()
        original_rename = KEYS.rename_no_replace

        def collide_with_claim(source, destination):
            if source == target and destination == quarantine and not quarantine.exists():
                quarantine.write_bytes(competitor)
                quarantine.chmod(0o600)
            return original_rename(source, destination)

        with (
            mock.patch.object(KEYS, "reviewed_revision", return_value=REVISION),
            mock.patch.object(KEYS, "synthetic_recovery", return_value=evidence),
            mock.patch.object(KEYS, "rename_no_replace", side_effect=collide_with_claim),
        ):
            with self.assertRaisesRegex(ValueError, "changed before cleanup"):
                KEYS.verify_copy(
                    self.key_file, self.prepared, target, self.copy_a,
                    "a", "manager-copy", True
                )
        self.assertEqual(target.read_bytes(), original)
        self.assertEqual(quarantine.read_bytes(), competitor)

        target.unlink()
        quarantine.write_bytes(original)
        quarantine.chmod(0o600)
        live_competitor = b"competing live path survives\n"

        def collide_with_restore(source, destination):
            if source == quarantine and destination == target and not target.exists():
                target.write_bytes(live_competitor)
                target.chmod(0o600)
            return original_rename(source, destination)

        with mock.patch.object(KEYS, "rename_no_replace", side_effect=collide_with_restore):
            with self.assertRaisesRegex(ValueError, "manual repair"):
                KEYS.verify_copy(
                    self.key_file, self.prepared, target, self.copy_a,
                    "a", "manager-copy", True
                )
        self.assertEqual(target.read_bytes(), live_competitor)
        self.assertEqual(quarantine.read_bytes(), original)

    def test_stale_exact_write_converges_and_conflict_fails(self):
        target = self.config / "state"
        temporary = self.config / ".state.new"
        temporary.write_bytes(b"exact\n")
        temporary.chmod(0o600)
        KEYS.write_once(target, b"exact\n")
        self.assertEqual(target.read_bytes(), b"exact\n")
        with self.assertRaisesRegex(ValueError, "conflicts"):
            KEYS.write_once(target, b"different\n")

    def test_interrupted_key_write_is_adopted_without_new_entropy(self):
        temporary = self.config / ".keys.env.new"
        raw = (
            f"{KEYS.KEY_NAMES[0]}={'8' * 64}\n"
            f"{KEYS.KEY_NAMES[1]}={'9' * 64}\n"
        ).encode()
        temporary.write_bytes(raw)
        temporary.chmod(0o600)
        with (
            mock.patch.object(KEYS, "reviewed_revision", return_value=REVISION),
            mock.patch.object(KEYS.secrets, "token_hex") as entropy,
        ):
            KEYS.initialize(self.key_file, self.prepared)
        entropy.assert_not_called()
        self.assertEqual(self.key_file.read_bytes(), raw)
        self.key_file.unlink()
        with self.assertRaisesRegex(ValueError, "without its active key"):
            KEYS.initialize(self.key_file, self.prepared)

    def test_real_synthetic_crypto_uses_both_keys(self):
        values = {
            KEYS.KEY_NAMES[0]: "6" * 64,
            KEYS.KEY_NAMES[1]: "7" * 64,
        }
        package, descriptor = KEYS.synthetic_recovery(values)
        self.assertRegex(package, r"^[0-9a-f]{64}$")
        self.assertRegex(descriptor, r"^[0-9a-f]{64}$")

    def test_cli_output_does_not_contain_generated_keys(self):
        command = [
            "python3", str(SOURCE), "staging", "prepare",
            "--key-file", str(self.key_file),
            "--prepared-receipt", str(self.prepared),
        ]
        result = subprocess.run(
            command,
            cwd=ROOT,
            env={
                **os.environ,
                "SIDEYARD_RECOVERY_KEY_TEST_MODE": "1",
                "SIDEYARD_RECOVERY_KEY_TEST_REVISION": REVISION,
            },
            text=True,
            capture_output=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        values, _ = KEYS.read_keys(self.key_file)
        combined = result.stdout + result.stderr
        self.assertNotIn(values[KEYS.KEY_NAMES[0]], combined)
        self.assertNotIn(values[KEYS.KEY_NAMES[1]], combined)
        self.assertEqual(json.loads(result.stdout)["phase"], "local_prepared")


if __name__ == "__main__":
    unittest.main()
