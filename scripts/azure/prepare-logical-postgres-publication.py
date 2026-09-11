#!/usr/bin/env python3
"""Fetch and seal one journal-bound staging PostgreSQL recovery package locally."""
import argparse
from datetime import datetime, timezone
import fcntl
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import selectors
import shutil
import signal
import stat
import subprocess
import sys
import tarfile
import tempfile
import time


ROOT = Path(__file__).resolve().parents[2]
STORE_SOURCE = Path(__file__).with_name("logical-backup-store.py")
SPEC = importlib.util.spec_from_file_location("sideyard_logical_backup_store", STORE_SOURCE)
STORE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(STORE)
KEYS_SOURCE = Path(__file__).with_name("logical-recovery-keys.py")
KEYS_SPEC = importlib.util.spec_from_file_location("sideyard_logical_recovery_keys", KEYS_SOURCE)
KEYS = importlib.util.module_from_spec(KEYS_SPEC)
KEYS_SPEC.loader.exec_module(KEYS)
UUID = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}")
SHA = re.compile(r"[0-9a-f]{64}")
REVISION = re.compile(r"[0-9a-f]{40}")
IMAGE = re.compile(r"ghcr\.io/wmichelin/clubdue@sha256:[0-9a-f]{64}")
JOURNAL_FIELDS = {
    "format", "phase", "environment", "generation", "revision", "image", "attempt",
    "path", "package_sha256", "package_bytes", "export_id", "root_sha256",
}


def private_path(path, *, directory=False):
    info = path.lstat()
    expected = stat.S_ISDIR if directory else stat.S_ISREG
    mode = 0o700 if directory else 0o600
    if stat.S_ISLNK(info.st_mode) or not expected(info.st_mode) or info.st_uid != os.geteuid() or stat.S_IMODE(info.st_mode) != mode:
        raise ValueError(f"logical recovery {'directory' if directory else 'file'} is unsafe")
    if not directory and info.st_nlink != 1:
        raise ValueError("logical recovery file must have one link")


def private_directory(path):
    if not path.exists():
        path.mkdir(parents=True, mode=0o700)
    private_path(path, directory=True)


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":")).encode() + b"\n"


def fsync_directory(path):
    descriptor = os.open(path, os.O_RDONLY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def reviewed_revision_and_helper():
    override = os.environ.get("SIDEYARD_RECOVERY_EXPECTED_REVISION", "")
    if override and os.environ.get("SIDEYARD_RECOVERY_TEST_MODE") == "1" and REVISION.fullmatch(override):
        return override, Path(__file__).with_name("logical-export-handoff.sh").read_bytes()
    result = subprocess.run(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True, capture_output=True, check=True)
    revision = result.stdout.strip()
    if not REVISION.fullmatch(revision):
        raise ValueError("reviewed recovery revision is invalid")
    reviewed = [
        "scripts/azure/prepare-logical-postgres-publication.py",
        "scripts/azure/logical-export-handoff.sh",
        "scripts/azure/logical-backup-store.py",
        "scripts/azure/logical-recovery-keys.py",
    ]
    blobs = {}
    for path in reviewed:
        result = subprocess.run(["git", "show", f"HEAD:{path}"], cwd=ROOT, capture_output=True)
        local = ROOT / path
        if result.returncode or not local.is_file() or local.is_symlink() or local.read_bytes() != result.stdout:
            raise ValueError("logical recovery tooling does not match the reviewed revision")
        blobs[path] = result.stdout
    return revision, blobs["scripts/azure/logical-export-handoff.sh"]


def validate_journal(value, revision):
    patterns = {
        "generation": UUID, "revision": REVISION, "image": IMAGE, "attempt": UUID,
        "export_id": UUID, "package_sha256": SHA, "root_sha256": SHA,
    }
    if not isinstance(value, dict) or set(value) != JOURNAL_FIELDS or value.get("format") != 1 or value.get("phase") != "package_ready" or value.get("environment") != "staging":
        raise ValueError("host logical export is not package_ready")
    if any(not isinstance(value.get(name), str) or not pattern.fullmatch(value[name]) for name, pattern in patterns.items()):
        raise ValueError("host logical export identity is invalid")
    if value["revision"] != revision or value["image"].split("@", 1)[0] != "ghcr.io/wmichelin/clubdue":
        raise ValueError("host logical export does not match the reviewed revision")
    if type(value.get("package_bytes")) is not int or not 1 <= value["package_bytes"] <= STORE.MAX_PACKAGE:
        raise ValueError("host logical export size is invalid")
    if value["path"] != f"/var/lib/sideyard-logical-export/recovery-{value['export_id']}.tar":
        raise ValueError("host logical export path is invalid")
    return value


def validate_ssh_target(target, test_mode=False):
    if not re.fullmatch(r"[A-Za-z0-9._-]+", target):
        raise ValueError("SSH target must be a pinned host alias")
    if not test_mode and target != "pantry-droplet":
        raise ValueError("staging logical recovery requires the pinned pantry-droplet SSH alias")


def terminate_process_group(process):
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    process.wait()


def fetch_claim(target, revision, helper_source, destination):
    validate_ssh_target(target, os.environ.get("SIDEYARD_RECOVERY_TEST_MODE") == "1")
    ssh = shutil.which("ssh")
    if os.environ.get("SIDEYARD_RECOVERY_TEST_MODE") == "1" and os.environ.get("SIDEYARD_RECOVERY_SSH"):
        ssh = os.environ["SIDEYARD_RECOVERY_SSH"]
    if not ssh:
        raise ValueError("OpenSSH is required for logical recovery handoff")
    command = [ssh, "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=10", "-o", "ServerAliveCountMax=3", target, "bash -s -- staging claim"]
    timeout = 900
    if os.environ.get("SIDEYARD_RECOVERY_TEST_MODE") == "1" and os.environ.get("SIDEYARD_RECOVERY_SSH_TIMEOUT"):
        timeout = int(os.environ["SIDEYARD_RECOVERY_SSH_TIMEOUT"])
    process = subprocess.Popen(command, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
    journal, header, body_size, digest = None, bytearray(), 0, hashlib.sha256()
    error = bytearray()
    deadline = time.monotonic() + timeout
    try:
        process.stdin.write(helper_source); process.stdin.close()
        streams = selectors.DefaultSelector()
        streams.register(process.stdout, selectors.EVENT_READ, "stdout")
        streams.register(process.stderr, selectors.EVENT_READ, "stderr")
        while streams.get_map():
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise ValueError("logical recovery SSH claim exceeded its bounded transfer window")
            events = streams.select(min(remaining, 1))
            if not events and process.poll() is not None:
                continue
            for key, _ in events:
                data = os.read(key.fileobj.fileno(), 64 * 1024)
                if not data:
                    streams.unregister(key.fileobj)
                    continue
                if key.data == "stderr":
                    if len(error) + len(data) > 16 * 1024:
                        raise ValueError("logical recovery SSH claim produced excessive diagnostics")
                    error.extend(data)
                    continue
                pending = data
                if journal is None:
                    header.extend(pending)
                    newline = header.find(b"\n")
                    if newline < 0:
                        if len(header) > 32768:
                            raise ValueError("logical recovery SSH claim header is invalid")
                        destination.write(data)
                        continue
                    header_bytes = bytes(header[:newline + 1]); pending = bytes(header[newline + 1:]); header.clear()
                    try:
                        journal = json.loads(header_bytes)
                    except json.JSONDecodeError:
                        raise ValueError("logical recovery SSH claim header is invalid") from None
                    if header_bytes != canonical(journal):
                        raise ValueError("logical recovery SSH claim header is not canonical")
                    validate_journal(journal, revision)
                body_size += len(pending)
                if body_size > journal["package_bytes"] or body_size > STORE.MAX_PACKAGE:
                    raise ValueError("logical recovery SSH package exceeds its journal")
                digest.update(pending)
                destination.write(data)
        return_code = process.wait(timeout=max(1, deadline - time.monotonic()))
    except (BrokenPipeError, subprocess.TimeoutExpired, ValueError) as failure:
        terminate_process_group(process)
        if isinstance(failure, ValueError): raise
        raise ValueError("logical recovery SSH claim exceeded its bounded transfer window") from None
    finally:
        if process.poll() is None:
            terminate_process_group(process)
    if return_code:
        raise ValueError(f"logical recovery SSH claim failed with exit status {return_code}")
    if journal is None:
        raise ValueError("logical recovery SSH claim header is invalid")
    destination.flush()
    os.fsync(destination.fileno())
    package_offset = len(canonical(journal))
    if body_size != journal["package_bytes"] or digest.hexdigest() != journal["package_sha256"]:
        raise ValueError("logical recovery SSH package does not match its journal")
    return journal, package_offset


def read_manifest(package, offset, journal):
    package.seek(offset)
    with tarfile.open(fileobj=package, mode="r|") as archive:
        first = archive.next()
        if first is None or first.name != "manifest.json" or not first.isfile() or first.mode != 0o600 or first.size > 16 * 1024 * 1024:
            raise ValueError("logical package manifest member is invalid")
        reader = archive.extractfile(first)
        raw = reader.read() if reader else b""
        try:
            manifest = json.loads(raw)
        except json.JSONDecodeError:
            raise ValueError("logical package manifest is invalid") from None
        if raw != json.dumps(manifest, separators=(",", ":")).encode():
            raise ValueError("logical package manifest is not canonical")
        second = archive.next()
        if second is None or second.name != "database.ndjson" or not second.isfile() or second.mode != 0o600 or second.size < 1 or archive.next() is not None:
            raise ValueError("logical package members are invalid")
    required = {
        "type", "format", "environment", "generation", "source_backend", "source_identity",
        "source_artifact", "release_artifact", "logical_schema", "catalog_sha256", "migration_ledger",
        "export_id", "snapshot_started_at", "snapshot_completed_at", "archive_sha256", "archive_bytes",
        "root_sha256", "photo_source", "assets",
    }
    if not isinstance(manifest, dict) or set(manifest) != required or manifest["type"] != "logical-package" or manifest["format"] != 1:
        raise ValueError("logical package manifest identity is invalid")
    expected_source = f"postgres:sideyard_staging_logical_exporter@clubdue-staging-db:5432/clubdue#revision={journal['revision']}"
    matches = (
        manifest["environment"] == journal["environment"] == "staging"
        and manifest["generation"] == journal["generation"]
        and manifest["export_id"] == journal["export_id"]
        and manifest["root_sha256"] == journal["root_sha256"]
        and manifest["source_backend"] == "postgres"
        and manifest["source_identity"] == expected_source
        and manifest["source_artifact"] == journal["image"]
        and manifest["release_artifact"] == journal["image"]
        and manifest["logical_schema"] == "sideyard-2026-09-v31"
    )
    if not matches:
        raise ValueError("logical package manifest does not match its host journal")
    try:
        created = datetime.fromisoformat(manifest["snapshot_started_at"].replace("Z", "+00:00")).astimezone(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    except (AttributeError, ValueError):
        raise ValueError("logical package snapshot time is invalid") from None
    descriptor = {
        "format": 1, "environment": "staging", "generation": manifest["generation"],
        "created": created, "export_id": manifest["export_id"],
        "release_artifact": manifest["release_artifact"], "logical_schema": manifest["logical_schema"],
        "catalog_sha256": manifest["catalog_sha256"], "root_sha256": manifest["root_sha256"],
        "package_sha256": journal["package_sha256"], "package_bytes": journal["package_bytes"],
        "photo_source": manifest["photo_source"], "assets": manifest["assets"],
    }
    return STORE.validate_descriptor(descriptor)


def read_keys(path):
    private_path(path)
    values = {}
    for line in path.read_text().splitlines():
        key, separator, value = line.partition("=")
        if not separator or key in values:
            raise ValueError("logical recovery key file is invalid")
        values[key] = value
    expected = {"SIDEYARD_LOGICAL_ENCRYPTION_PASSPHRASE", "SIDEYARD_LOGICAL_AUTHENTICATION_KEY"}
    if set(values) != expected or any(not SHA.fullmatch(value) for value in values.values()) or values["SIDEYARD_LOGICAL_ENCRYPTION_PASSPHRASE"] == values["SIDEYARD_LOGICAL_AUTHENTICATION_KEY"]:
        raise ValueError("logical recovery keys must be distinct 256-bit hex values")
    return values["SIDEYARD_LOGICAL_ENCRYPTION_PASSPHRASE"].encode(), bytes.fromhex(values["SIDEYARD_LOGICAL_AUTHENTICATION_KEY"])


def atomic_write(path, content):
    temporary = path.with_name("." + path.name + ".new")
    descriptor = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        with os.fdopen(descriptor, "wb") as output:
            output.write(content); output.flush(); os.fsync(output.fileno())
        os.replace(temporary, path); fsync_directory(path.parent)
    finally:
        if temporary.exists(): temporary.unlink()


def seal(package, offset, folder, descriptor, passphrase, authentication_key):
    ciphertext = folder / "recovery.tar.enc"
    sidecar = Path(str(ciphertext) + ".mac")
    descriptor_path = folder / "descriptor.json"
    allowed = {
        "recovery.tar.enc", "recovery.tar.enc.mac", "descriptor.json", "source-journal.json",
        "publication-journal.json", "completion-receipt.json",
        ".recovery.tar.enc.new", ".recovery.tar.enc.mac.new", ".descriptor.json.new", ".source-journal.json.new",
    }
    for path in folder.iterdir():
        abandoned = path.name.removeprefix("publication-abandoned-").removesuffix(".json")
        if path.name not in allowed and not (path.name.startswith("publication-abandoned-") and path.name.endswith(".json") and UUID.fullmatch(abandoned)):
            raise ValueError("logical publication folder has unexpected contents")
        private_path(path)
        if path.name.startswith("."): path.unlink()
    if all(path.exists() for path in (ciphertext, sidecar, descriptor_path)):
        for path in (ciphertext, sidecar, descriptor_path): private_path(path)
        if descriptor_path.read_bytes() != canonical(descriptor):
            raise ValueError("existing logical publication descriptor does not match")
        STORE.verify_pair(ciphertext, authentication_key, descriptor)
        verify_decryption(ciphertext, descriptor, passphrase, folder)
        return ciphertext
    if descriptor_path.exists() and descriptor_path.read_bytes() != canonical(descriptor):
        raise ValueError("partial logical publication descriptor does not match")
    if (sidecar.exists() or descriptor_path.exists()) and not ciphertext.exists():
        raise ValueError("partial logical publication is missing its ciphertext")
    if not ciphertext.exists():
        temporary = folder / ".recovery.tar.enc.new"
        output_fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        read_fd, write_fd = os.pipe()
        try:
            os.write(write_fd, passphrase + b"\n"); os.close(write_fd); write_fd = -1
            package.seek(offset)
            with os.fdopen(output_fd, "wb") as output:
                result = subprocess.run(
                    ["openssl", "enc", "-aes-256-cbc", "-salt", "-pbkdf2", "-iter", "200000", "-md", "sha256", "-pass", f"fd:{read_fd}"],
                    stdin=package, stdout=output, stderr=subprocess.PIPE, pass_fds=(read_fd,), check=False,
                )
                output.flush(); os.fsync(output.fileno())
            if result.returncode:
                raise ValueError(f"logical recovery encryption failed with exit status {result.returncode}")
            os.replace(temporary, ciphertext); fsync_directory(folder)
        finally:
            if write_fd >= 0: os.close(write_fd)
            os.close(read_fd)
            if temporary.exists(): temporary.unlink()
    verify_decryption(ciphertext, descriptor, passphrase, folder)
    if sidecar.exists():
        STORE.verify_pair(ciphertext, authentication_key, descriptor)
    else:
        atomic_write(sidecar, STORE.PAIR_MAGIC + STORE.pair_digest(ciphertext, authentication_key, descriptor))
    STORE.verify_pair(ciphertext, authentication_key, descriptor)
    if not descriptor_path.exists(): atomic_write(descriptor_path, canonical(descriptor))
    return ciphertext


def verify_decryption(ciphertext, descriptor, passphrase, folder):
    read_fd, write_fd = os.pipe()
    try:
        os.write(write_fd, passphrase + b"\n"); os.close(write_fd); write_fd = -1
        with tempfile.TemporaryFile(dir=folder) as diagnostics:
            process = subprocess.Popen(
                ["openssl", "enc", "-d", "-aes-256-cbc", "-pbkdf2", "-iter", "200000", "-md", "sha256", "-pass", f"fd:{read_fd}", "-in", str(ciphertext)],
                stdout=subprocess.PIPE, stderr=diagnostics, pass_fds=(read_fd,),
            )
            digest=hashlib.sha256(); size=0
            while True:
                block=process.stdout.read(1024*1024)
                if not block: break
                size+=len(block)
                if size>descriptor["package_bytes"]:
                    process.kill(); process.wait()
                    raise ValueError("logical recovery decrypted package exceeds its descriptor")
                digest.update(block)
            process.stdout.close()
            return_code=process.wait()
            if return_code:
                raise ValueError("logical recovery ciphertext does not decrypt with the current key")
            if size!=descriptor["package_bytes"] or digest.hexdigest()!=descriptor["package_sha256"]:
                raise ValueError("logical recovery decrypted package does not match its descriptor")
    finally:
        if write_fd>=0: os.close(write_fd)
        os.close(read_fd)


def acquire_lock(root):
    path = root / ".publication.lock"
    descriptor = os.open(path, os.O_RDWR | os.O_CREAT | getattr(os, "O_NOFOLLOW", 0), 0o600)
    handle = os.fdopen(descriptor, "r+")
    private_path(path)
    try:
        fcntl.flock(handle.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        handle.close()
        raise ValueError("another logical publication operation is active") from None
    return handle


def required_free_space():
    # The anonymous downloaded package and its encrypted output coexist. Decryption
    # is verified as a stream, so it does not allocate a third package-sized file.
    return STORE.MAX_PACKAGE + STORE.MAX_CIPHERTEXT + 64 * 1024 * 1024


def require_free_space(root):
    if shutil.disk_usage(root).free < required_free_space():
        raise ValueError("logical recovery work directory has insufficient free space")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("environment", choices=("staging",))
    parser.add_argument("--ssh-target", default="pantry-droplet")
    parser.add_argument("--work-root", type=Path, default=Path.home()/".local"/"share"/"sideyard-logical-recovery"/"staging")
    parser.add_argument("--key-file", type=Path, default=Path.home()/".config"/"sideyard-azure"/"staging-logical-recovery.env")
    parser.add_argument("--custody-attestation", type=Path, default=Path.home()/".config"/"sideyard-azure"/"staging-logical-recovery-custody.json")
    args = parser.parse_args()
    os.umask(0o077)
    private_directory(args.work_root)
    require_free_space(args.work_root)
    lock = acquire_lock(args.work_root)
    try:
        passphrase, authentication_key, _ = KEYS.read_verified_keys(
            args.key_file, args.custody_attestation
        )
        revision, helper_source = reviewed_revision_and_helper()
        with tempfile.TemporaryFile(dir=args.work_root) as package:
            journal, offset = fetch_claim(args.ssh_target, revision, helper_source, package)
            descriptor = read_manifest(package, offset, journal)
            folder = args.work_root / journal["export_id"]
            private_directory(folder)
            ciphertext = seal(package, offset, folder, descriptor, passphrase, authentication_key)
            source_journal = folder / "source-journal.json"
            if source_journal.exists():
                private_path(source_journal)
                if source_journal.read_bytes() != canonical(journal):
                    raise ValueError("existing logical source journal does not match the host export")
            else:
                atomic_write(source_journal, canonical(journal))
    finally:
        lock.close()
    print(json.dumps({"environment":"staging","export_id":journal["export_id"],"phase":"sealed","ciphertext":str(ciphertext)},sort_keys=True,separators=(",",":")))


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, subprocess.SubprocessError, tarfile.TarError) as error:
        print(str(error), file=sys.stderr)
        raise SystemExit(65)
