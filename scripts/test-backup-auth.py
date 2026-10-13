#!/usr/bin/env python3
"""Security regressions: wrong keys and unauthenticated legacy files fail closed."""
import hashlib
import hmac
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent


class BackupAuthenticationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.file = Path(self.temp.name) / "backup.sql.enc"
        self.file.write_bytes(b"fixture ciphertext\x00\xff")

    def run_auth(self, mode, key="fixture-key-one"):
        return subprocess.run(
            [sys.executable, str(ROOT / "backup-auth.py"), mode, str(self.file)],
            env={**os.environ, "CLUBDUE_BACKUP_HMAC_KEY": key},
            capture_output=True,
        )

    def sign(self):
        signed = self.run_auth("sign")
        self.assertEqual(signed.returncode, 0, signed.stderr)
        self.file.with_suffix(".enc.mac").write_bytes(signed.stdout)
        return signed.stdout

    def test_right_key_works_and_wrong_key_fails(self):
        one = self.sign()
        two = self.run_auth("sign", "fixture-key-two")
        self.assertEqual(two.returncode, 0)
        self.assertNotEqual(one, two.stdout)
        self.assertEqual(self.run_auth("verify").returncode, 0)
        self.assertEqual(self.run_auth("verify", "fixture-key-two").returncode, 65)

    def test_modified_ciphertext_and_sidecar_fail(self):
        original = self.sign()
        self.file.write_bytes(self.file.read_bytes() + b"changed")
        self.assertEqual(self.run_auth("verify").returncode, 65)
        self.file.with_suffix(".enc.mac").write_bytes(original[:-1])
        self.assertEqual(self.run_auth("verify").returncode, 65)

    def test_missing_secret_and_missing_sidecar_fail(self):
        self.assertEqual(self.run_auth("sign", "").returncode, 65)
        self.assertEqual(self.run_auth("verify").returncode, 65)

    def test_legacy_requires_explicit_recovery(self):
        legacy = hmac.new(b"file:/dev/fd/3", self.file.read_bytes(), hashlib.sha256).digest()
        self.file.with_suffix(".enc.mac").write_bytes(legacy)
        result = self.run_auth("verify")
        self.assertEqual(result.returncode, 65)
        self.assertIn(b"explicit isolated recovery", result.stderr)
        recovery = self.run_auth("verify-legacy")
        self.assertEqual(recovery.returncode, 0)
        self.assertIn(b"no trustworthy authentication", recovery.stderr)

    def test_wrong_key_is_rejected_before_docker_or_decryption(self):
        self.sign()
        bin_dir = Path(self.temp.name) / "bin"
        bin_dir.mkdir()
        sentinel = Path(self.temp.name) / "unexpected-effect"
        for name in ["docker", "openssl"]:
            program = bin_dir / name
            program.write_text('#!/bin/sh\ntouch "$SENTINEL"\nexit 99\n')
            program.chmod(0o700)
        result = subprocess.run(
            ["bash", str(ROOT / "postgres-restore.sh"), str(self.file)],
            env={**os.environ, "PATH": str(bin_dir) + os.pathsep + os.environ["PATH"],
                 "SENTINEL": str(sentinel), "CLUBDUE_RESTORE_DB_CONTAINER": "fixture-restore",
                 "CLUBDUE_BACKUP_PASSPHRASE": "fixture-passphrase",
                 "CLUBDUE_BACKUP_HMAC_KEY": "wrong-key"},
            capture_output=True,
        )
        self.assertEqual(result.returncode, 65, result.stderr)
        self.assertFalse(sentinel.exists(), "restore touched external dependencies before authentication")

    def test_legacy_container_prefix_does_not_bypass_isolation(self):
        legacy = hmac.new(b"file:/dev/fd/3", self.file.read_bytes(), hashlib.sha256).digest()
        self.file.with_suffix(".enc.mac").write_bytes(legacy)
        bin_dir = Path(self.temp.name) / "unsafe-bin"
        bin_dir.mkdir()
        sentinel = Path(self.temp.name) / "decryption-started"
        docker = bin_dir / "docker"
        docker.write_text('#!/bin/sh\ncat "$INSPECT_FIXTURE"\n')
        docker.chmod(0o700)
        openssl = bin_dir / "openssl"
        openssl.write_text('#!/bin/sh\ntouch "$SENTINEL"\nexit 99\n')
        openssl.chmod(0o700)
        fixture = Path(self.temp.name) / "inspect.json"
        cases = [
            {"HostConfig": {"NetworkMode": "host"}, "Mounts": []},
            {"HostConfig": {"NetworkMode": "none", "Privileged": True}, "Mounts": []},
            {"HostConfig": {"NetworkMode": "none"}, "Mounts": [{"Type": "volume"}]},
        ]
        for unsafe in cases:
            with self.subTest(container=unsafe):
                fixture.write_text(json.dumps([unsafe]))
                result = subprocess.run(
                    ["bash", str(ROOT / "postgres-restore.sh"), "--legacy-unauthenticated", str(self.file)],
                    env={**os.environ, "PATH": str(bin_dir) + os.pathsep + os.environ["PATH"],
                         "INSPECT_FIXTURE": str(fixture), "SENTINEL": str(sentinel),
                         "CLUBDUE_RESTORE_DB_CONTAINER": "sideyard-legacy-recovery-unsafe",
                         "CLUBDUE_BACKUP_PASSPHRASE": "fixture-passphrase",
                         "CLUBDUE_BACKUP_HMAC_KEY": "fixture-key"}, capture_output=True,
                )
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(b"tmpfs-only", result.stderr)
                self.assertFalse(sentinel.exists())

    def test_wrong_passphrase_does_not_access_target_database(self):
        encrypted = subprocess.run(
            ["openssl", "enc", "-aes-256-cbc", "-salt", "-pbkdf2", "-pass",
             "env:CLUBDUE_BACKUP_PASSPHRASE", "-out", str(self.file)],
            input=b"--\n-- PostgreSQL database dump\n--\n" + b"CREATE TABLE recovery_test (id integer);\n" * 1000,
            env={**os.environ, "CLUBDUE_BACKUP_PASSPHRASE": "correct-passphrase"},
            capture_output=True,
        )
        self.assertEqual(encrypted.returncode, 0, encrypted.stderr)
        self.sign()
        bin_dir = Path(self.temp.name) / "restore-bin"
        bin_dir.mkdir()
        sentinel = Path(self.temp.name) / "target-accessed"
        docker = bin_dir / "docker"
        docker.write_text('#!/bin/sh\ncat >"$SENTINEL"\n')
        docker.chmod(0o700)
        env = {**os.environ, "PATH": str(bin_dir) + os.pathsep + os.environ["PATH"],
               "SENTINEL": str(sentinel), "CLUBDUE_RESTORE_DB_CONTAINER": "fixture-restore",
               "CLUBDUE_BACKUP_PASSPHRASE": "wrong-passphrase",
               "CLUBDUE_BACKUP_HMAC_KEY": "fixture-key-one"}
        failed = subprocess.run(["bash", str(ROOT / "postgres-restore.sh"), str(self.file)],
                                env=env, capture_output=True)
        self.assertNotEqual(failed.returncode, 0)
        self.assertIn(b"target database was not accessed", failed.stderr)
        self.assertFalse(sentinel.exists())
        env["CLUBDUE_BACKUP_PASSPHRASE"] = "correct-passphrase"
        restored = subprocess.run(["bash", str(ROOT / "postgres-restore.sh"), str(self.file)],
                                  env=env, capture_output=True)
        self.assertEqual(restored.returncode, 0, restored.stderr)
        self.assertTrue(sentinel.exists())
        self.assertEqual(sentinel.read_bytes(),
                         b"--\n-- PostgreSQL database dump\n--\n" + b"CREATE TABLE recovery_test (id integer);\n" * 1000)


if __name__ == "__main__":
    unittest.main()
