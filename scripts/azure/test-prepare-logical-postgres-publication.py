#!/usr/bin/env python3
"""Focused end-to-end checks for local logical publication preparation."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import time
from unittest import mock
import unittest


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "azure" / "prepare-logical-postgres-publication.py"
SPEC=importlib.util.spec_from_file_location("prepare_logical_publication",SCRIPT)
PREPARE=importlib.util.module_from_spec(SPEC); SPEC.loader.exec_module(PREPARE)
REVISION = "a" * 40
IMAGE = "ghcr.io/wmichelin/clubdue@sha256:" + "b" * 64
GENERATION = "123e4567-e89b-42d3-a456-426614174000"
ATTEMPT = "223e4567-e89b-42d3-a456-426614174000"
EXPORT = "323e4567-e89b-42d3-a456-426614174000"
ACCOUNT = "sideyardstg1234"
RESOURCE = f"/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/sideyard-staging/providers/Microsoft.Storage/storageAccounts/{ACCOUNT}"


class PrepareLogicalPublicationTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.work = self.root / "work"
        self.work.mkdir(mode=0o700)
        self.keys = self.root / "keys.env"
        self.passphrase = "c" * 64
        self.auth = "d" * 64
        self.keys.write_text(
            f"SIDEYARD_LOGICAL_ENCRYPTION_PASSPHRASE={self.passphrase}\n"
            f"SIDEYARD_LOGICAL_AUTHENTICATION_KEY={self.auth}\n"
        )
        self.keys.chmod(0o600)
        prepared_receipt=self.root/"staging-logical-recovery-prepared.json"
        copy_a=self.root/"staging-logical-recovery-copy-a.json"
        copy_b=self.root/"staging-logical-recovery-copy-b.json"
        self.attestation=self.root/"staging-logical-recovery-custody.json"
        with mock.patch.object(PREPARE.KEYS,"reviewed_revision",return_value=REVISION):
            PREPARE.KEYS.initialize(self.keys,prepared_receipt)
        for slot,label,receipt in (("a","manager-copy",copy_a),("b","offline-media",copy_b)):
            retrieved_folder=self.root/f"retrieved-{slot}"; retrieved_folder.mkdir(mode=0o700)
            retrieved=retrieved_folder/"keys.env"; retrieved.write_bytes(self.keys.read_bytes()); retrieved.chmod(0o600)
            evidence=PREPARE.KEYS.expected_synthetic_digests()
            with mock.patch.object(PREPARE.KEYS,"reviewed_revision",return_value=REVISION),mock.patch.object(PREPARE.KEYS,"synthetic_recovery",return_value=evidence):
                PREPARE.KEYS.verify_copy(self.keys,prepared_receipt,retrieved,receipt,slot,label)
        with mock.patch.object(PREPARE.KEYS,"reviewed_revision",return_value=REVISION):
            PREPARE.KEYS.attest(self.keys,prepared_receipt,copy_a,copy_b,self.attestation,True)
        self.package = self.root / "package.tar"
        self.manifest = {
            "type":"logical-package","format":1,"environment":"staging","generation":GENERATION,
            "source_backend":"postgres","source_identity":f"postgres:sideyard_staging_logical_exporter@clubdue-staging-db:5432/clubdue#revision={REVISION}",
            "source_artifact":IMAGE,"release_artifact":IMAGE,"logical_schema":"sideyard-2026-09-v31",
            "catalog_sha256":"e"*64,"migration_ledger":[],"export_id":EXPORT,
            "snapshot_started_at":"2026-09-11T03:00:00.000000Z","snapshot_completed_at":"2026-09-11T03:00:01.000000Z",
            "archive_sha256":"f"*64,"archive_bytes":2,"root_sha256":"1"*64,
            "photo_source":{"kind":"azure_blob","account":ACCOUNT,"container":"photos","resource_id":RESOURCE},"assets":[],
        }
        with tarfile.open(self.package, "w", format=tarfile.USTAR_FORMAT) as archive:
            for name, body in (("manifest.json", json.dumps(self.manifest,separators=(",",":")).encode()), ("database.ndjson", b"{}")):
                info=tarfile.TarInfo(name); info.mode=0o600; info.size=len(body); info.mtime=0
                import io
                archive.addfile(info,io.BytesIO(body))
        package_bytes=self.package.read_bytes()
        self.journal={
            "format":1,"phase":"package_ready","environment":"staging","generation":GENERATION,"revision":REVISION,
            "image":IMAGE,"attempt":ATTEMPT,"path":f"/var/lib/sideyard-logical-export/recovery-{EXPORT}.tar",
            "package_sha256":hashlib.sha256(package_bytes).hexdigest(),"package_bytes":len(package_bytes),"export_id":EXPORT,"root_sha256":"1"*64,
        }
        self.claim=self.root/"claim"
        self.write_claim(self.journal,package_bytes)
        self.ssh=self.root/"ssh"
        self.ssh.write_text('#!/bin/sh\ncat >/dev/null\nprintf "%s\\n" "$*" >> "$SIDEYARD_TEST_SSH_LOG"\ncat "$SIDEYARD_TEST_CLAIM"\n')
        self.ssh.chmod(0o700)
        self.log=self.root/"ssh.log"

    def write_claim(self,journal,package):
        self.claim.write_bytes(json.dumps(journal,sort_keys=True,separators=(",",":")).encode()+b"\n"+package)

    def run_prepare(self, extra_environment=None, target=None, custody=True):
        environment={**os.environ,"SIDEYARD_RECOVERY_TEST_MODE":"1","SIDEYARD_RECOVERY_EXPECTED_REVISION":REVISION,"SIDEYARD_RECOVERY_SSH":str(self.ssh),"SIDEYARD_TEST_CLAIM":str(self.claim),"SIDEYARD_TEST_SSH_LOG":str(self.log)}
        environment.update(extra_environment or {})
        command=["python3",str(SCRIPT),"staging","--work-root",str(self.work),"--key-file",str(self.keys)]
        command.extend(["--custody-attestation",str(self.attestation if custody else self.root/"missing-custody.json")])
        if target: command.extend(["--ssh-target",target])
        return subprocess.run(command,cwd=ROOT,env=environment,text=True,capture_output=True)

    def test_fetches_anonymous_plaintext_and_creates_verified_encrypted_pair(self):
        result=self.run_prepare()
        self.assertEqual(result.returncode,0,result.stderr)
        output=json.loads(result.stdout); self.assertEqual(output["phase"],"sealed")
        folder=self.work/EXPORT
        self.assertEqual({p.name for p in folder.iterdir()},{"recovery.tar.enc","recovery.tar.enc.mac","descriptor.json","source-journal.json"})
        for path in folder.iterdir(): self.assertEqual(path.stat().st_mode&0o777,0o600)
        self.assertEqual((folder/"source-journal.json").read_bytes(), PREPARE.canonical(self.journal))
        decrypted=self.root/"decrypted.tar"
        subprocess.run(["openssl","enc","-d","-aes-256-cbc","-pbkdf2","-iter","200000","-md","sha256","-pass","env:SIDEYARD_TEST_PASS","-in",str(folder/"recovery.tar.enc"),"-out",str(decrypted)],env={**os.environ,"SIDEYARD_TEST_PASS":self.passphrase},check=True,capture_output=True)
        self.assertEqual(decrypted.read_bytes(),self.package.read_bytes())
        stale=folder/".descriptor.json.new"; stale.write_text("interrupted"); stale.chmod(0o600)
        rerun=self.run_prepare(); self.assertEqual(rerun.returncode,0,rerun.stderr)
        self.assertFalse(stale.exists())
        (folder/"descriptor.json").unlink()
        resumed=self.run_prepare(); self.assertEqual(resumed.returncode,0,resumed.stderr)
        self.assertTrue((folder/"descriptor.json").is_file())
        (folder/"source-journal.json").write_text("{}\n"); (folder/"source-journal.json").chmod(0o600)
        changed_journal=self.run_prepare(); self.assertNotEqual(changed_journal.returncode,0)
        self.assertIn("source journal does not match",changed_journal.stderr)
        log=self.log.read_text(); self.assertIn("ServerAliveCountMax=3 pantry-droplet bash -s -- staging claim",log)
        self.assertNotIn(self.passphrase,log); self.assertNotIn(self.auth,log)
        self.keys.write_text(f"SIDEYARD_LOGICAL_ENCRYPTION_PASSPHRASE={'9'*64}\nSIDEYARD_LOGICAL_AUTHENTICATION_KEY={self.auth}\n")
        self.keys.chmod(0o600)
        wrong_key=self.run_prepare(); self.assertNotEqual(wrong_key.returncode,0)
        self.assertIn("custody does not match",wrong_key.stderr)

    def test_missing_custody_blocks_before_ssh_or_plaintext(self):
        result=self.run_prepare(custody=False)
        self.assertNotEqual(result.returncode,0)
        self.assertIn("logical recovery file is unavailable",result.stderr)
        self.assertFalse(self.log.exists())
        self.assertEqual(list(self.work.iterdir()),[self.work/".publication.lock"])

    def test_changed_package_is_rejected_without_linked_plaintext(self):
        changed=bytearray(self.package.read_bytes()); changed[-1]^=1
        self.write_claim(self.journal,bytes(changed))
        result=self.run_prepare()
        self.assertNotEqual(result.returncode,0)
        self.assertFalse((self.work/EXPORT).exists())
        self.assertNotIn(self.passphrase,result.stderr); self.assertNotIn(self.auth,result.stderr)

    def test_manifest_mismatch_fails_before_encrypted_state(self):
        self.journal["root_sha256"]="2"*64
        self.write_claim(self.journal,self.package.read_bytes())
        result=self.run_prepare()
        self.assertNotEqual(result.returncode,0)
        self.assertFalse((self.work/EXPORT).exists())

    def test_stalled_ssh_is_killed_at_the_outer_deadline(self):
        process_id=self.root/"stalled.pid"
        self.ssh.write_text('#!/bin/sh\ncat >/dev/null\necho $$ > "$SIDEYARD_TEST_STALLED_PID"\nsleep 30\n')
        self.ssh.chmod(0o700)
        started=time.monotonic()
        result=self.run_prepare({"SIDEYARD_RECOVERY_SSH_TIMEOUT":"1","SIDEYARD_TEST_STALLED_PID":str(process_id)})
        self.assertNotEqual(result.returncode,0)
        self.assertLess(time.monotonic()-started,5)
        self.assertIn("bounded transfer window",result.stderr)
        self.assertEqual(list(self.work.iterdir()),[self.work/".publication.lock"])
        with self.assertRaises(ProcessLookupError): os.kill(int(process_id.read_text()),0)

    def test_non_staging_ssh_alias_is_rejected(self):
        with self.assertRaisesRegex(ValueError,"pinned pantry-droplet"):
            PREPARE.validate_ssh_target("other-known-host")

    def test_free_space_threshold_covers_simultaneous_package_and_ciphertext(self):
        expected = PREPARE.STORE.MAX_PACKAGE + PREPARE.STORE.MAX_CIPHERTEXT + 64 * 1024 * 1024
        self.assertEqual(PREPARE.required_free_space(), expected)
        with mock.patch.object(PREPARE.shutil, "disk_usage", return_value=mock.Mock(free=expected - 1)):
            with self.assertRaisesRegex(ValueError, "insufficient free space"):
                PREPARE.require_free_space(self.work)
        with mock.patch.object(PREPARE.shutil, "disk_usage", return_value=mock.Mock(free=expected)):
            PREPARE.require_free_space(self.work)


if __name__ == "__main__": unittest.main()
