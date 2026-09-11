#!/usr/bin/env python3
"""Prepare and verify the staging logical-recovery key custody boundary."""

import argparse
from datetime import datetime, timezone
import fcntl
import hashlib
import hmac
import importlib.util
import json
import os
from pathlib import Path
import re
import secrets
import stat
import subprocess
import sys
import tempfile
import uuid


HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
KEY_NAMES = (
    "SIDEYARD_LOGICAL_ENCRYPTION_PASSPHRASE",
    "SIDEYARD_LOGICAL_AUTHENTICATION_KEY",
)
SHA = re.compile(r"^[0-9a-f]{64}$")
REVISION = re.compile(r"^[0-9a-f]{40}$")
LABEL = re.compile(r"^[a-z][a-z0-9-]{2,31}$")
PREPARED_FIELDS = {
    "format", "phase", "environment", "kit_id", "revision", "prepared_at",
    "key_file_sha256", "encryption_key_id", "authentication_key_id",
}
COPY_FIELDS = PREPARED_FIELDS | {
    "custody_slot", "custody_label", "retrieved_at", "synthetic_package_sha256",
    "synthetic_descriptor_sha256", "copy_hmac",
}
ATTESTATION_FIELDS = PREPARED_FIELDS | {
    "copy_a_receipt_sha256", "copy_b_receipt_sha256", "copy_a_label",
    "copy_b_label", "copy_a_retrieved_at", "copy_b_retrieved_at",
    "attestation_revision", "attested_at", "attestation_hmac",
}
ATTESTATION_MAGIC = b"Sideyard logical recovery custody attestation v1\n"
COPY_MAGIC = b"Sideyard logical recovery custody copy v1\n"
SYNTHETIC_PACKAGE = b"sideyard synthetic recovery custody check\n"


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":")).encode() + b"\n"


def now():
    return datetime.now(timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")


def private_directory(path, *, create=False):
    try:
        if create and not path.exists():
            path.mkdir(parents=True, mode=0o700)
        details = path.lstat()
    except OSError:
        raise ValueError("logical recovery directory is unavailable") from None
    if (
        stat.S_ISLNK(details.st_mode)
        or not stat.S_ISDIR(details.st_mode)
        or details.st_uid != os.getuid()
        or stat.S_IMODE(details.st_mode) != 0o700
    ):
        raise ValueError("logical recovery directory must be owner-only")


def private_file(path, *, minimum=1, maximum=16 * 1024):
    try:
        details = path.lstat()
    except OSError:
        raise ValueError("logical recovery file is unavailable") from None
    if (
        stat.S_ISLNK(details.st_mode)
        or not stat.S_ISREG(details.st_mode)
        or details.st_nlink != 1
        or details.st_uid != os.getuid()
        or stat.S_IMODE(details.st_mode) != 0o600
        or not minimum <= details.st_size <= maximum
    ):
        raise ValueError("logical recovery file must be a private regular file")
    return details


def read_private_bytes(path, *, minimum=1, maximum=16 * 1024):
    flags = os.O_RDONLY | os.O_CLOEXEC | getattr(os, "O_NOFOLLOW", 0)
    try:
        descriptor = os.open(path, flags)
    except OSError:
        raise ValueError("logical recovery file is unavailable") from None
    try:
        details = os.fstat(descriptor)
        if (
            not stat.S_ISREG(details.st_mode)
            or details.st_nlink != 1
            or details.st_uid != os.getuid()
            or stat.S_IMODE(details.st_mode) != 0o600
            or not minimum <= details.st_size <= maximum
        ):
            raise ValueError("logical recovery file must be a private regular file")
        with os.fdopen(descriptor, "rb", closefd=False) as source:
            raw = source.read(maximum + 1)
        if len(raw) != details.st_size or len(raw) > maximum:
            raise ValueError("logical recovery file changed while it was read")
        return raw, details
    finally:
        os.close(descriptor)


def fsync_directory(folder):
    descriptor = os.open(folder, os.O_RDONLY | getattr(os, "O_DIRECTORY", 0))
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def write_once(path, content):
    private_directory(path.parent)
    if path.exists() or path.is_symlink():
        existing, _ = read_private_bytes(path)
        if existing != content:
            raise ValueError("logical recovery state conflicts with existing state")
        return
    temporary = path.with_name("." + path.name + ".new")
    if temporary.exists() or temporary.is_symlink():
        try:
            existing, _ = read_private_bytes(temporary)
            if existing == content:
                os.replace(temporary, path)
                fsync_directory(path.parent)
                return
        except ValueError:
            pass
        temporary.unlink()
        fsync_directory(path.parent)
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0)
    descriptor = os.open(temporary, flags, 0o600)
    try:
        with os.fdopen(descriptor, "wb") as output:
            output.write(content)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, path)
        fsync_directory(path.parent)
    finally:
        if temporary.exists():
            temporary.unlink()


def read_keys(path, *, with_details=False):
    raw, details = read_private_bytes(path, maximum=1024)
    try:
        text = raw.decode("ascii")
    except UnicodeDecodeError:
        raise ValueError("logical recovery key file is invalid") from None
    values = {}
    for line in text.splitlines():
        name, separator, value = line.partition("=")
        if not separator or name in values:
            raise ValueError("logical recovery key file is invalid")
        values[name] = value
    if (
        set(values) != set(KEY_NAMES)
        or any(not SHA.fullmatch(values[name]) for name in KEY_NAMES)
        or values[KEY_NAMES[0]] == values[KEY_NAMES[1]]
    ):
        raise ValueError("logical recovery keys must be distinct 256-bit hex values")
    expected = "".join(f"{name}={values[name]}\n" for name in KEY_NAMES).encode()
    if raw != expected:
        raise ValueError("logical recovery key file is not canonical")
    if with_details:
        return values, raw, details
    return values, raw


def key_id(purpose, value):
    return hashlib.sha256(
        b"sideyard/logical-recovery-key/v1\0" + purpose.encode() + b"\0" + bytes.fromhex(value)
    ).hexdigest()


def key_metadata(values, raw):
    encryption_id = key_id("encryption", values[KEY_NAMES[0]])
    authentication_id = key_id("authentication", values[KEY_NAMES[1]])
    digest = hashlib.sha256(
        b"sideyard/logical-recovery-kit/v1\0"
        + bytes.fromhex(encryption_id)
        + bytes.fromhex(authentication_id)
    ).digest()
    kit_id = str(uuid.UUID(bytes=digest[:16], version=5))
    return {
        "kit_id": kit_id,
        "key_file_sha256": hashlib.sha256(raw).hexdigest(),
        "encryption_key_id": encryption_id,
        "authentication_key_id": authentication_id,
    }


def reviewed_revision():
    override = os.environ.get("SIDEYARD_RECOVERY_KEY_TEST_REVISION")
    if override and os.environ.get("SIDEYARD_RECOVERY_KEY_TEST_MODE") == "1" and REVISION.fullmatch(override):
        return override
    revision = subprocess.run(
        ["git", "rev-parse", "HEAD"], cwd=ROOT, check=True, text=True, capture_output=True
    ).stdout.strip()
    if not REVISION.fullmatch(revision):
        raise ValueError("logical recovery revision is invalid")
    reviewed = (
        "scripts/azure/logical-recovery-keys.py",
        "scripts/azure/prepare-logical-postgres-publication.py",
        "scripts/azure/logical-backup-store.py",
    )
    for name in reviewed:
        content = subprocess.run(
            ["git", "show", f"HEAD:{name}"], cwd=ROOT, check=True, capture_output=True
        ).stdout
        if content != (ROOT / name).read_bytes():
            raise ValueError("logical recovery tooling must match reviewed HEAD")
    return revision


def acquire_lock(folder):
    lock_path = folder / ".logical-recovery-keys.lock"
    descriptor = os.open(
        lock_path,
        os.O_RDWR | os.O_CREAT | os.O_CLOEXEC | getattr(os, "O_NOFOLLOW", 0),
        0o600,
    )
    handle = os.fdopen(descriptor, "r+")
    private_file(lock_path, minimum=0)
    fcntl.flock(handle.fileno(), fcntl.LOCK_EX)
    return handle


def prepared_document(key_file, values, raw, revision):
    details = private_file(key_file, maximum=1024)
    return {
        "format": 1,
        "phase": "local_prepared",
        "environment": "staging",
        **key_metadata(values, raw),
        "revision": revision,
        "prepared_at": datetime.fromtimestamp(details.st_mtime, timezone.utc)
        .isoformat(timespec="seconds")
        .replace("+00:00", "Z"),
    }


def read_document(path, fields, phase):
    raw, _ = read_private_bytes(path)
    try:
        value = json.loads(raw)
    except json.JSONDecodeError:
        raise ValueError("logical recovery receipt is invalid") from None
    if (
        raw != canonical(value)
        or not isinstance(value, dict)
        or set(value) != fields
        or value.get("format") != 1
        or value.get("phase") != phase
        or value.get("environment") != "staging"
        or not re.fullmatch(r"[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}", str(value.get("kit_id", "")))
        or not REVISION.fullmatch(str(value.get("revision", "")))
        or any(not SHA.fullmatch(str(value.get(name, ""))) for name in (
            "key_file_sha256", "encryption_key_id", "authentication_key_id"
        ))
    ):
        raise ValueError("logical recovery receipt is invalid")
    timestamp_fields = {"prepared_at"}
    if fields == COPY_FIELDS:
        if (
            value.get("custody_slot") not in ("a", "b")
            or not LABEL.fullmatch(str(value.get("custody_label", "")))
            or any(not SHA.fullmatch(str(value.get(name, ""))) for name in (
                "synthetic_package_sha256", "synthetic_descriptor_sha256", "copy_hmac"
            ))
        ):
            raise ValueError("logical recovery copy receipt is invalid")
        timestamp_fields.add("retrieved_at")
    if fields == ATTESTATION_FIELDS:
        if (
            value.get("copy_a_label") == value.get("copy_b_label")
            or not LABEL.fullmatch(str(value.get("copy_a_label", "")))
            or not LABEL.fullmatch(str(value.get("copy_b_label", "")))
            or any(not SHA.fullmatch(str(value.get(name, ""))) for name in (
                "copy_a_receipt_sha256", "copy_b_receipt_sha256", "attestation_hmac"
            ))
            or not REVISION.fullmatch(str(value.get("attestation_revision", "")))
        ):
            raise ValueError("logical recovery custody attestation is invalid")
        timestamp_fields.update(("copy_a_retrieved_at", "copy_b_retrieved_at", "attested_at"))
    for name in timestamp_fields:
        try:
            parsed = datetime.fromisoformat(str(value[name]).replace("Z", "+00:00"))
        except (KeyError, ValueError):
            raise ValueError("logical recovery receipt timestamp is invalid") from None
        if parsed.tzinfo is None:
            raise ValueError("logical recovery receipt timestamp is invalid")
    return value


def stable_receipt(path, candidate, fields, phase, timestamp):
    if path.exists() or path.is_symlink():
        existing = read_document(path, fields, phase)
        comparison = dict(candidate)
        comparison[timestamp] = existing[timestamp]
        if existing != comparison:
            raise ValueError("logical recovery receipt conflicts with existing state")
        return existing
    write_once(path, canonical(candidate))
    return read_document(path, fields, phase)


def initialize(key_file, prepared_receipt):
    private_directory(key_file.parent)
    if key_file.exists() or key_file.is_symlink():
        values, raw = read_keys(key_file)
    else:
        if prepared_receipt.exists() or prepared_receipt.is_symlink():
            raise ValueError("prepared recovery state exists without its active key file")
        temporary = key_file.with_name("." + key_file.name + ".new")
        if temporary.exists() or temporary.is_symlink():
            try:
                values, raw, temporary_details = read_keys(temporary, with_details=True)
            except ValueError:
                temporary.unlink()
                fsync_directory(key_file.parent)
            else:
                current = temporary.lstat()
                if (current.st_dev, current.st_ino) != (temporary_details.st_dev, temporary_details.st_ino):
                    raise ValueError("interrupted recovery key file changed before adoption")
                os.replace(temporary, key_file)
                fsync_directory(key_file.parent)
        if not key_file.exists():
            values = {name: secrets.token_hex(32) for name in KEY_NAMES}
            if values[KEY_NAMES[0]] == values[KEY_NAMES[1]]:
                raise ValueError("logical recovery entropy returned duplicate keys")
            raw = "".join(f"{name}={values[name]}\n" for name in KEY_NAMES).encode()
            write_once(key_file, raw)
        values, raw = read_keys(key_file)
    metadata = prepared_document(key_file, values, raw, reviewed_revision())
    if prepared_receipt.exists() or prepared_receipt.is_symlink():
        verified = read_document(prepared_receipt, PREPARED_FIELDS, "local_prepared")
        for name in PREPARED_FIELDS - {"revision"}:
            if verified[name] != metadata[name]:
                raise ValueError("logical recovery preparation receipt does not match the active keys")
        return verified
    write_once(prepared_receipt, canonical(metadata))
    verified = read_document(prepared_receipt, PREPARED_FIELDS, "local_prepared")
    if verified != metadata:
        raise ValueError("logical recovery preparation receipt does not match the active keys")
    return verified


def load_module(name, path):
    specification = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(specification)
    specification.loader.exec_module(module)
    return module


def synthetic_descriptor(store):
    package_sha = hashlib.sha256(SYNTHETIC_PACKAGE).hexdigest()
    account = "sideyardcustody"
    return store.validate_descriptor({
        "format": 1,
        "environment": "staging",
        "generation": "123e4567-e89b-42d3-a456-426614174000",
        "created": "20260911T000000Z",
        "export_id": "223e4567-e89b-42d3-a456-426614174000",
        "release_artifact": "ghcr.io/wmichelin/clubdue@sha256:" + "a" * 64,
        "logical_schema": "sideyard-2026-09-v31",
        "catalog_sha256": "b" * 64,
        "root_sha256": "c" * 64,
        "package_sha256": package_sha,
        "package_bytes": len(SYNTHETIC_PACKAGE),
        "photo_source": {
            "kind": "azure_blob",
            "account": account,
            "container": "photos",
            "resource_id": "/subscriptions/11111111-1111-1111-1111-111111111111/"
            "resourceGroups/sideyard-staging/providers/Microsoft.Storage/"
            f"storageAccounts/{account}",
        },
        "assets": [],
    })


def expected_synthetic_digests():
    prepare = load_module("sideyard_custody_expected", HERE / "prepare-logical-postgres-publication.py")
    descriptor = synthetic_descriptor(prepare.STORE)
    return hashlib.sha256(SYNTHETIC_PACKAGE).hexdigest(), hashlib.sha256(canonical(descriptor)).hexdigest()


def synthetic_recovery(values):
    prepare = load_module("sideyard_custody_prepare", HERE / "prepare-logical-postgres-publication.py")
    store = prepare.STORE
    descriptor = synthetic_descriptor(store)
    package_sha = hashlib.sha256(SYNTHETIC_PACKAGE).hexdigest()
    with tempfile.TemporaryDirectory(prefix="sideyard-custody-") as folder_name:
        folder = Path(folder_name)
        folder.chmod(0o700)
        with tempfile.TemporaryFile(dir=folder) as package:
            package.write(SYNTHETIC_PACKAGE)
            package.flush()
            package.seek(0)
            ciphertext = prepare.seal(
                package,
                0,
                folder,
                descriptor,
                values[KEY_NAMES[0]].encode(),
                bytes.fromhex(values[KEY_NAMES[1]]),
            )
            prepare.verify_decryption(ciphertext, descriptor, values[KEY_NAMES[0]].encode(), folder)
            store.verify_pair(ciphertext, bytes.fromhex(values[KEY_NAMES[1]]), descriptor)
    return package_sha, hashlib.sha256(canonical(descriptor)).hexdigest()


def validate_copy_receipt(document, authentication_key):
    payload = {name: value for name, value in document.items() if name != "copy_hmac"}
    expected = hmac.new(
        authentication_key,
        COPY_MAGIC + canonical(payload),
        hashlib.sha256,
    ).hexdigest()
    if not hmac.compare_digest(document["copy_hmac"], expected):
        raise ValueError("custody copy receipt authentication failed")
    package_sha, descriptor_sha = expected_synthetic_digests()
    if (
        document["synthetic_package_sha256"] != package_sha
        or document["synthetic_descriptor_sha256"] != descriptor_sha
    ):
        raise ValueError("custody copy receipt synthetic recovery evidence is invalid")
    return document


def cleanup_retrieved(path, verified_details):
    try:
        current = path.lstat()
    except OSError:
        raise ValueError("retrieved custody file changed before cleanup") from None
    if (current.st_dev, current.st_ino) != (verified_details.st_dev, verified_details.st_ino):
        raise ValueError("retrieved custody file changed before cleanup")
    path.unlink()
    fsync_directory(path.parent)


def verify_copy(key_file, prepared_receipt, retrieved_key_file, copy_receipt, slot, label, delete_retrieved=False):
    if slot not in ("a", "b") or not LABEL.fullmatch(label):
        raise ValueError("custody copy requires slot a or b and a normalized non-sensitive label")
    prepared = read_document(prepared_receipt, PREPARED_FIELDS, "local_prepared")
    values, raw, primary = read_keys(key_file, with_details=True)
    current = key_metadata(values, raw)
    if any(current[name] != prepared[name] for name in current):
        raise ValueError("prepared receipt does not match the active key kit")
    existing = None
    if copy_receipt.exists() or copy_receipt.is_symlink():
        existing = read_document(copy_receipt, COPY_FIELDS, "copy_verified")
        for name in PREPARED_FIELDS - {"phase", "revision"}:
            if existing[name] != prepared[name]:
                raise ValueError("custody copy receipt does not match the prepared key kit")
        if existing["custody_slot"] != slot or existing["custody_label"] != label:
            raise ValueError("custody copy receipt conflicts with the requested copy")
        validate_copy_receipt(existing, bytes.fromhex(values[KEY_NAMES[1]]))
        if not retrieved_key_file.exists() and not retrieved_key_file.is_symlink():
            return existing
    private_directory(retrieved_key_file.parent)
    retrieved_values, retrieved_raw, retrieved = read_keys(retrieved_key_file, with_details=True)
    if (primary.st_dev, primary.st_ino) == (retrieved.st_dev, retrieved.st_ino):
        raise ValueError("retrieved custody file must be independent from the active key file")
    if not hmac.compare_digest(raw, retrieved_raw):
        raise ValueError("retrieved custody file does not match the active key file")
    metadata = key_metadata(retrieved_values, retrieved_raw)
    if any(metadata[name] != prepared[name] for name in metadata):
        raise ValueError("retrieved custody file does not match the prepared key kit")
    revision = reviewed_revision()
    package_sha, descriptor_sha = synthetic_recovery(retrieved_values)
    if existing is not None:
        if (
            existing["synthetic_package_sha256"] != package_sha
            or existing["synthetic_descriptor_sha256"] != descriptor_sha
        ):
            raise ValueError("custody copy receipt conflicts with the recovery check")
        if delete_retrieved:
            cleanup_retrieved(retrieved_key_file, retrieved)
        return existing
    payload = {
        **prepared,
        "phase": "copy_verified",
        "revision": revision,
        "custody_slot": slot,
        "custody_label": label,
        "retrieved_at": now(),
        "synthetic_package_sha256": package_sha,
        "synthetic_descriptor_sha256": descriptor_sha,
    }
    candidate = {
        **payload,
        "copy_hmac": hmac.new(
            bytes.fromhex(retrieved_values[KEY_NAMES[1]]),
            COPY_MAGIC + canonical(payload),
            hashlib.sha256,
        ).hexdigest(),
    }
    document = stable_receipt(
        copy_receipt,
        candidate,
        COPY_FIELDS,
        "copy_verified",
        "retrieved_at",
    )
    if delete_retrieved:
        cleanup_retrieved(retrieved_key_file, retrieved)
    return document


def attest(key_file, prepared_receipt, copy_a_receipt, copy_b_receipt, attestation, owner_confirmed):
    if not owner_confirmed:
        raise ValueError("off-device custody requires the owner's explicit confirmation")
    prepared = read_document(prepared_receipt, PREPARED_FIELDS, "local_prepared")
    values, raw = read_keys(key_file)
    current = key_metadata(values, raw)
    if any(current[name] != prepared[name] for name in current):
        raise ValueError("prepared receipt does not match the active key kit")
    copy_a = read_document(copy_a_receipt, COPY_FIELDS, "copy_verified")
    copy_b = read_document(copy_b_receipt, COPY_FIELDS, "copy_verified")
    authentication_key = bytes.fromhex(values[KEY_NAMES[1]])
    validate_copy_receipt(copy_a, authentication_key)
    validate_copy_receipt(copy_b, authentication_key)
    if copy_a["custody_slot"] != "a" or copy_b["custody_slot"] != "b":
        raise ValueError("custody copies do not occupy distinct required slots")
    if copy_a["custody_label"] == copy_b["custody_label"]:
        raise ValueError("custody copies require distinct independent labels")
    for copy in (copy_a, copy_b):
        for name in PREPARED_FIELDS - {"phase", "revision"}:
            if copy[name] != prepared[name]:
                raise ValueError("custody copy does not match the prepared key kit")
    if attestation.exists() or attestation.is_symlink():
        return verify_active_custody(key_file, attestation)
    payload = {
        **prepared,
        "phase": "custody_verified",
        "copy_a_receipt_sha256": hashlib.sha256(canonical(copy_a)).hexdigest(),
        "copy_b_receipt_sha256": hashlib.sha256(canonical(copy_b)).hexdigest(),
        "copy_a_label": copy_a["custody_label"],
        "copy_b_label": copy_b["custody_label"],
        "copy_a_retrieved_at": copy_a["retrieved_at"],
        "copy_b_retrieved_at": copy_b["retrieved_at"],
        "attestation_revision": reviewed_revision(),
        "attested_at": now(),
    }
    candidate = {
        **payload,
        "attestation_hmac": hmac.new(
            authentication_key,
            ATTESTATION_MAGIC + canonical(payload),
            hashlib.sha256,
        ).hexdigest(),
    }
    return stable_receipt(
        attestation,
        candidate,
        ATTESTATION_FIELDS,
        "custody_verified",
        "attested_at",
    )


def verify_active_custody(key_file, attestation):
    _, _, _, verified = read_verified_keys(key_file, attestation, include_receipt=True)
    return verified


def read_verified_keys(key_file, attestation, *, include_receipt=False):
    if key_file.parent.resolve() != attestation.parent.resolve():
        raise ValueError("recovery keys and custody evidence must share one private directory")
    values, raw = read_keys(key_file)
    metadata = key_metadata(values, raw)
    verified = read_document(attestation, ATTESTATION_FIELDS, "custody_verified")
    if any(metadata[name] != verified[name] for name in metadata):
        raise ValueError("off-device custody does not match the active recovery keys")
    verify_active_custody_values(verified, bytes.fromhex(values[KEY_NAMES[1]]), attestation)
    result = (
        values[KEY_NAMES[0]].encode(),
        bytes.fromhex(values[KEY_NAMES[1]]),
        metadata,
    )
    return (*result, verified) if include_receipt else result


def verify_active_custody_values(verified, authentication_key, attestation):
    if (
        verified["copy_a_label"] == verified["copy_b_label"]
        or not LABEL.fullmatch(verified["copy_a_label"])
        or not LABEL.fullmatch(verified["copy_b_label"])
        or any(not SHA.fullmatch(verified[name]) for name in (
            "copy_a_receipt_sha256", "copy_b_receipt_sha256"
        ))
        or not REVISION.fullmatch(verified["attestation_revision"])
    ):
        raise ValueError("off-device custody attestation is invalid")
    payload = {name: value for name, value in verified.items() if name != "attestation_hmac"}
    expected_hmac = hmac.new(
        authentication_key,
        ATTESTATION_MAGIC + canonical(payload),
        hashlib.sha256,
    ).hexdigest()
    if not hmac.compare_digest(verified["attestation_hmac"], expected_hmac):
        raise ValueError("off-device custody attestation authentication failed")
    copy_a_path = attestation.parent / "staging-logical-recovery-copy-a.json"
    copy_b_path = attestation.parent / "staging-logical-recovery-copy-b.json"
    copy_a = read_document(copy_a_path, COPY_FIELDS, "copy_verified")
    copy_b = read_document(copy_b_path, COPY_FIELDS, "copy_verified")
    validate_copy_receipt(copy_a, authentication_key)
    validate_copy_receipt(copy_b, authentication_key)
    if (
        copy_a["custody_slot"] != "a"
        or copy_b["custody_slot"] != "b"
        or copy_a["custody_label"] != verified["copy_a_label"]
        or copy_b["custody_label"] != verified["copy_b_label"]
        or copy_a["retrieved_at"] != verified["copy_a_retrieved_at"]
        or copy_b["retrieved_at"] != verified["copy_b_retrieved_at"]
        or hashlib.sha256(canonical(copy_a)).hexdigest() != verified["copy_a_receipt_sha256"]
        or hashlib.sha256(canonical(copy_b)).hexdigest() != verified["copy_b_receipt_sha256"]
    ):
        raise ValueError("off-device custody copy evidence does not match the attestation")
    for copy in (copy_a, copy_b):
        for name in PREPARED_FIELDS - {"phase", "revision"}:
            if copy[name] != verified[name]:
                raise ValueError("off-device custody copy evidence uses a different key kit")
    return verified


def public_result(document):
    return {
        "environment": "staging",
        "phase": document["phase"],
        "kit_id": document["kit_id"],
        "encryption_key_id": document["encryption_key_id"],
        "authentication_key_id": document["authentication_key_id"],
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("environment", choices=("staging",))
    parser.add_argument("action", choices=("prepare", "verify-copy", "attest-off-device", "check"))
    parser.add_argument("--key-file", type=Path, default=Path.home()/".config"/"sideyard-azure"/"staging-logical-recovery.env")
    parser.add_argument("--prepared-receipt", type=Path, default=Path.home()/".config"/"sideyard-azure"/"staging-logical-recovery-prepared.json")
    parser.add_argument("--retrieved-key-file", type=Path)
    parser.add_argument("--copy-slot", choices=("a", "b"))
    parser.add_argument("--copy-label")
    parser.add_argument("--delete-retrieved-copy", action="store_true")
    parser.add_argument("--attestation", type=Path, default=Path.home()/".config"/"sideyard-azure"/"staging-logical-recovery-custody.json")
    parser.add_argument("--owner-confirmed-off-device", action="store_true")
    args = parser.parse_args()
    os.umask(0o077)
    private_directory(args.key_file.parent)
    lock = acquire_lock(args.key_file.parent)
    try:
        if args.action == "prepare":
            document = initialize(args.key_file, args.prepared_receipt)
            receipt = args.prepared_receipt
        elif args.action == "verify-copy":
            if args.retrieved_key_file is None or args.copy_slot is None or args.copy_label is None:
                parser.error("verify-copy requires --retrieved-key-file, --copy-slot and --copy-label")
            receipt = args.key_file.parent/f"staging-logical-recovery-copy-{args.copy_slot}.json"
            document = verify_copy(args.key_file, args.prepared_receipt, args.retrieved_key_file, receipt, args.copy_slot, args.copy_label, args.delete_retrieved_copy)
        elif args.action == "attest-off-device":
            document = attest(
                args.key_file,
                args.prepared_receipt,
                args.key_file.parent/"staging-logical-recovery-copy-a.json",
                args.key_file.parent/"staging-logical-recovery-copy-b.json",
                args.attestation,
                args.owner_confirmed_off_device,
            )
            receipt = args.attestation
        else:
            document = verify_active_custody(args.key_file, args.attestation)
            receipt = args.attestation
    finally:
        lock.close()
    print(json.dumps(public_result(document), sort_keys=True, separators=(",", ":")))


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        print(str(error), file=sys.stderr)
        raise SystemExit(65)
