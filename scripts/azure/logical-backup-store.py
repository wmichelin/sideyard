#!/usr/bin/env python3
"""Authenticated, version-pinned Azure storage for Sideyard logical recovery packages.

This is an internal library. It deliberately has no command-line entry point or
credential discovery: operators must supply separate backup and photo clients.
"""
from email.utils import formatdate, parsedate_to_datetime
from datetime import datetime
import hashlib
import hmac
import io
import json
import os
from pathlib import Path
import re
import stat
import urllib.error
import urllib.parse
import urllib.request


MAX_PACKAGE = 250 * 1024 * 1024
MAX_CIPHERTEXT = 256 * 1024 * 1024
MAX_COMPLETION = 32 * 1024
MAX_PHOTO = 4 * 1024 * 1024
MAX_PUBLICATION_SECONDS = 60 * 60
COMPLETION_HEADROOM_SECONDS = 5 * 60
PAIR_MAGIC = b"SYLOGICAL1\n"
COMPLETION_MAGIC = b"Sideyard logical completion v1\n"
ENCRYPTION = "openssl-aes-256-cbc-pbkdf2-sha256"
SHA256 = re.compile(r"[a-f0-9]{64}")
UUID = re.compile(r"[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}")
TIMESTAMP = re.compile(r"\d{8}T\d{6}Z")
ACCOUNT = re.compile(r"sideyard[a-z0-9]{1,16}")
LOGICAL_KEY = re.compile(
    r"logical-(components|completions)/(staging)/([a-f0-9-]{36})/"
    r"(\d{8}T\d{6}Z)-([a-f0-9-]{36})/"
    r"(recovery\.tar\.enc(?:\.mac)?|completion\.json)"
)


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()


def file_digest(path):
    result = hashlib.sha256()
    with path.open("rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            result.update(block)
    return result.digest()


def descriptor_digest(descriptor):
    return hashlib.sha256(canonical(descriptor)).digest()


def pair_digest(path, secret, descriptor):
    binding = descriptor_digest(descriptor)
    result = hmac.new(secret, PAIR_MAGIC + binding, hashlib.sha256)
    with path.open("rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            result.update(block)
    return binding + result.digest()


def verify_pair(path, secret, descriptor):
    size = path.stat().st_size
    if not secret or not 32 <= size <= MAX_CIPHERTEXT or size % 16:
        raise ValueError("logical backup secret and bounded ciphertext are required")
    with path.open("rb") as source:
        if source.read(8) != b"Salted__":
            raise ValueError("logical backup ciphertext is not the required salted OpenSSL envelope")
    sidecar = Path(str(path) + ".mac")
    if sidecar.stat().st_size != len(PAIR_MAGIC) + 2 * hashlib.sha256().digest_size:
        raise ValueError("logical backup sidecar has the wrong version or size")
    if not hmac.compare_digest(sidecar.read_bytes(), PAIR_MAGIC + pair_digest(path, secret, descriptor)):
        raise ValueError("logical backup authentication failed")


def storage_source(environment, account, resource_id, container):
    if environment != "staging" or not all(isinstance(value, str) for value in (account, resource_id, container)) or not ACCOUNT.fullmatch(account) or container not in ("backups", "photos"):
        raise ValueError("invalid Sideyard storage source")
    expected = (
        f"/subscriptions/{UUID.pattern}/resourceGroups/sideyard-{environment}/providers/"
        f"Microsoft\\.Storage/storageAccounts/{account}"
    )
    if not re.fullmatch(expected, resource_id):
        raise ValueError("storage source does not match the recovery environment")
    return {"account": account, "container": container, "resource_id": resource_id}


def validate_ref(ref, suffix, maximum):
    if not isinstance(ref, dict) or set(ref) != {"key", "version", "sha256", "size", "stored_at"}:
        raise ValueError("logical backup object reference has unknown or missing fields")
    match = LOGICAL_KEY.fullmatch(ref["key"]) if isinstance(ref["key"], str) else None
    expected_kind = "completions" if suffix == "completion.json" else "components"
    if not match or match[1] != expected_kind or not UUID.fullmatch(match[3]) or not UUID.fullmatch(match[5]) or match[6] != suffix or not isinstance(ref["version"], str) or not 1 <= len(ref["version"]) <= 512:
        raise ValueError("logical backup object reference is not version pinned")
    if not isinstance(ref["sha256"], str) or not SHA256.fullmatch(ref["sha256"]):
        raise ValueError("logical backup object reference has an invalid digest")
    if type(ref["size"]) is not int or not 1 <= ref["size"] <= maximum:
        raise ValueError("logical backup object reference has an invalid size")
    storage_time(ref["stored_at"])
    return match


def ref_identity(match):
    return match[2], match[3], match[4], match[5]


def storage_time(value):
    if not isinstance(value, str) or len(value) > 64:
        raise ValueError("logical backup object reference has an invalid storage time")
    try:
        parsed = parsedate_to_datetime(value)
    except (TypeError, ValueError):
        raise ValueError("logical backup object reference has an invalid storage time") from None
    if parsed.tzinfo is None:
        raise ValueError("logical backup object reference has an invalid storage time")
    return parsed.timestamp()


def validate_publication_span(files, marker):
    started = min(storage_time(ref["stored_at"]) for ref in files)
    completed = storage_time(marker["stored_at"])
    if completed < started or completed - started >= MAX_PUBLICATION_SECONDS:
        raise ValueError("logical backup completion falls outside its retention-safe window")


def validate_completion_start(files, server_now):
    started = min(storage_time(ref["stored_at"]) for ref in files)
    current = storage_time(server_now)
    if current < started or current - started >= MAX_PUBLICATION_SECONDS - COMPLETION_HEADROOM_SECONDS:
        raise ValueError("logical backup has insufficient retention-safe time to publish completion; create a fresh export ID")


def asset_root(assets):
    return hashlib.sha256(canonical(assets)).hexdigest()


def validate_descriptor(descriptor):
    fields = {
        "format", "environment", "generation", "created", "export_id",
        "release_artifact", "logical_schema", "catalog_sha256", "root_sha256",
        "package_sha256", "package_bytes", "photo_source", "assets",
    }
    if set(descriptor) != fields or type(descriptor["format"]) is not int or descriptor["format"] != 1:
        raise ValueError("invalid logical recovery descriptor")
    environment = descriptor["environment"]
    if environment != "staging":
        raise ValueError("logical Blob recovery is staging-only until production migration")
    if not isinstance(descriptor["generation"], str) or not isinstance(descriptor["export_id"], str) or not UUID.fullmatch(descriptor["generation"]) or not UUID.fullmatch(descriptor["export_id"]):
        raise ValueError("invalid logical recovery identity")
    if not isinstance(descriptor["created"], str) or not TIMESTAMP.fullmatch(descriptor["created"]):
        raise ValueError("invalid logical recovery timestamp")
    try:
        datetime.strptime(descriptor["created"], "%Y%m%dT%H%M%SZ")
    except ValueError:
        raise ValueError("invalid logical recovery timestamp") from None
    for name in ("catalog_sha256", "root_sha256", "package_sha256"):
        if not isinstance(descriptor[name], str) or not SHA256.fullmatch(descriptor[name]):
            raise ValueError(f"invalid {name}")
    if type(descriptor["package_bytes"]) is not int or not 1 <= descriptor["package_bytes"] <= MAX_PACKAGE:
        raise ValueError("invalid logical recovery package size")
    for name in ("release_artifact", "logical_schema"):
        if not isinstance(descriptor[name], str) or not 1 <= len(descriptor[name]) <= 512:
            raise ValueError(f"invalid {name}")
    source = descriptor["photo_source"]
    if not isinstance(source, dict) or set(source) != {"kind", "account", "container", "resource_id"} or source["kind"] != "azure_blob":
        raise ValueError("logical cloud recovery requires an Azure photo source")
    storage_source(environment, source["account"], source["resource_id"], source["container"])
    if source["container"] != "photos":
        raise ValueError("logical cloud recovery requires the photos container")
    assets = descriptor["assets"]
    if not isinstance(assets, list) or len(assets) > 100000:
        raise ValueError("logical recovery assets must be a list")
    seen, seen_photos = set(), set()
    expected_fields = {
        "object_key", "shop_id", "photo_id", "content_sha256", "content_type",
        "byte_length", "version_id", "created_at", "retired_at", "purged_at", "required",
    }
    for asset in assets:
        if not isinstance(asset, dict) or set(asset) != expected_fields:
            raise ValueError("invalid logical recovery photo asset")
        if not isinstance(asset["object_key"], str) or not SHA256.fullmatch(asset["object_key"]) or asset["object_key"] in seen:
            raise ValueError("invalid or repeated logical recovery photo key")
        seen.add(asset["object_key"])
        if not all(isinstance(asset[name], str) for name in ("shop_id", "photo_id")) or not UUID.fullmatch(asset["shop_id"]) or not UUID.fullmatch(asset["photo_id"]) or asset["photo_id"] in seen_photos:
            raise ValueError("invalid logical recovery photo identity")
        seen_photos.add(asset["photo_id"])
        if not isinstance(asset["content_sha256"], str) or not SHA256.fullmatch(asset["content_sha256"]) or asset["content_type"] not in ("image/jpeg", "image/png"):
            raise ValueError("invalid logical recovery photo content")
        if type(asset["byte_length"]) is not int or not 1 <= asset["byte_length"] <= MAX_PHOTO:
            raise ValueError("invalid logical recovery photo size")
        version = asset["version_id"]
        if version is not None and (not isinstance(version, str) or not 1 <= len(version) <= 512):
            raise ValueError("invalid logical recovery photo version")
        instants = {}
        for name in ("created_at", "retired_at", "purged_at"):
            value = asset[name]
            if value is None:
                instants[name] = None
                continue
            if not isinstance(value, str) or not re.fullmatch(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{6}Z", value):
                raise ValueError("invalid logical recovery photo lifecycle timestamp")
            try:
                instants[name] = datetime.strptime(value, "%Y-%m-%dT%H:%M:%S.%fZ")
            except ValueError:
                raise ValueError("invalid logical recovery photo lifecycle timestamp") from None
        if asset["created_at"] is None or (asset["purged_at"] is not None and (asset["retired_at"] is None or version is None)):
            raise ValueError("invalid logical recovery photo lifecycle")
        if instants["retired_at"] is not None and instants["retired_at"] < instants["created_at"] or instants["purged_at"] is not None and instants["purged_at"] < instants["retired_at"]:
            raise ValueError("invalid logical recovery photo lifecycle order")
        if not isinstance(asset["required"], bool) or asset["required"] != (version is not None and asset["purged_at"] is None):
            raise ValueError("logical recovery photo requirement is inconsistent")
    return descriptor


def seal_completion(payload, secret):
    if not secret:
        raise ValueError("logical backup completion secret is required")
    data = canonical(payload)
    return canonical({"payload": payload, "hmac": hmac.new(secret, COMPLETION_MAGIC + data, hashlib.sha256).hexdigest()})


def open_completion(data, secret, expected, backup):
    if not secret or len(data) > MAX_COMPLETION:
        raise ValueError("invalid logical backup completion marker")
    doc = json.loads(data)
    if not isinstance(doc, dict) or set(doc) != {"payload", "hmac"}:
        raise ValueError("invalid logical backup completion marker")
    if data != canonical(doc):
        raise ValueError("logical backup completion marker is not canonical")
    payload = doc["payload"]
    if not isinstance(payload, dict) or not hmac.compare_digest(
        str(doc["hmac"]), hmac.new(secret, COMPLETION_MAGIC + canonical(payload), hashlib.sha256).hexdigest()
    ):
        raise ValueError("logical backup completion authentication failed")
    fields = {
        "format", "environment", "generation", "created", "export_id", "release_artifact",
        "logical_schema", "catalog_sha256", "root_sha256", "package_sha256", "package_bytes",
        "encryption", "backup_source", "photo_source", "asset_count", "required_asset_count", "asset_root_sha256", "files",
    }
    if set(payload) != fields or type(payload["format"]) is not int or payload["format"] != 1 or payload["encryption"] != ENCRYPTION:
        raise ValueError("invalid logical backup completion payload")
    for name in ("environment", "generation", "created", "export_id", "release_artifact", "logical_schema", "catalog_sha256", "root_sha256", "photo_source"):
        if payload[name] != expected[name]:
            raise ValueError(f"logical backup completion {name} mismatch")
    if payload["backup_source"] != backup.source:
        raise ValueError("logical backup completion storage source mismatch")
    files = payload["files"]
    if not isinstance(files, list) or len(files) != 2:
        raise ValueError("logical backup completion must reference ciphertext and authentication sidecar")
    first = validate_ref(files[0], "recovery.tar.enc", MAX_CIPHERTEXT)
    second = validate_ref(files[1], "recovery.tar.enc.mac", len(PAIR_MAGIC) + 64)
    if ref_identity(first) != ref_identity(second) or files[0]["key"] + ".mac" != files[1]["key"]:
        raise ValueError("logical backup completion object pair mismatch")
    prefix = ref_identity(first)
    if prefix != (payload["environment"], payload["generation"], payload["created"], payload["export_id"]):
        raise ValueError("logical backup completion path mismatch")
    if not TIMESTAMP.fullmatch(payload["created"]) or not UUID.fullmatch(payload["export_id"]):
        raise ValueError("invalid logical backup completion identity")
    if payload["package_sha256"] != expected["package_sha256"] or payload["package_bytes"] != expected["package_bytes"]:
        raise ValueError("logical backup completion package mismatch")
    if type(payload["asset_count"]) is not int or type(payload["required_asset_count"]) is not int or type(payload["package_bytes"]) is not int:
        raise ValueError("logical backup completion numeric fields are invalid")
    if payload["asset_count"] != expected["asset_count"] or payload["required_asset_count"] != expected["required_asset_count"] or payload["asset_root_sha256"] != expected["asset_root_sha256"]:
        raise ValueError("logical backup completion photo inventory mismatch")
    return payload


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


class AzureBlob:
    """A container-scoped client. Supply a token issued for this client's identity."""

    def __init__(self, environment, account, container, resource_id, token, opener=None):
        self.source = storage_source(environment, account, resource_id, container)
        if not token:
            raise ValueError("Azure Blob bearer token is required")
        self.environment, self.account, self.container, self.token = environment, account, container, token
        self.opener = opener or urllib.request.build_opener(NoRedirect())

    def request(self, method, key, version=None, data=None, size=0, content_type="application/octet-stream"):
        if self.container == "backups":
            match = LOGICAL_KEY.fullmatch(key) if isinstance(key, str) else None
            valid_kind = match and ((match[1] == "components" and match[6] != "completion.json") or (match[1] == "completions" and match[6] == "completion.json"))
            if not match or match[2] != self.environment or not valid_kind:
                raise ValueError("invalid logical backup object name")
        elif not isinstance(key, str) or not SHA256.fullmatch(key):
            raise ValueError("invalid photo object name")
        url = f"https://{self.account}.blob.core.windows.net/{self.container}/{key}"
        if version:
            url += "?" + urllib.parse.urlencode({"versionid": version})
        headers = {"Authorization": "Bearer " + self.token, "x-ms-version": "2023-11-03", "x-ms-date": formatdate(usegmt=True)}
        if method == "PUT":
            headers.update({"x-ms-blob-type": "BlockBlob", "If-None-Match": "*", "Content-Length": str(size), "Content-Type": content_type})
        return self.opener.open(urllib.request.Request(url, data=data, method=method, headers=headers), timeout=120)

    def put(self, key, source, size, digest, content_type="application/octet-stream", require_new=False):
        try:
            with self.request("PUT", key, data=source, size=size, content_type=content_type) as response:
                version = response.headers.get("x-ms-version-id")
                stored_at = response.headers.get("Last-Modified")
        except urllib.error.HTTPError as error:
            if error.code not in (409, 412):
                raise ValueError(f"Azure logical backup upload failed (HTTP {error.code})") from None
            error.close()
            if require_new:
                raise ValueError("logical backup component already exists; create a fresh export ID") from None
            with self.request("HEAD", key) as response:
                version = response.headers.get("x-ms-version-id")
                stored_at = response.headers.get("Last-Modified")
        if not version or not stored_at:
            raise ValueError("Azure Blob versioning and trusted modification time are required")
        ref = {"key": key, "version": version, "sha256": digest, "size": size, "stored_at": stored_at}
        self.download(ref, None)
        return ref

    def download(self, ref, destination, expected_type=None):
        maximum = MAX_PHOTO if self.container == "photos" else MAX_CIPHERTEXT
        if self.container == "backups":
            validate_ref(ref, ref["key"].rsplit("/", 1)[-1], maximum)
        elif (
            not isinstance(ref, dict)
            or not isinstance(ref.get("key"), str)
            or not SHA256.fullmatch(ref["key"])
            or not isinstance(ref.get("version"), str)
            or not 1 <= len(ref["version"]) <= 512
            or not isinstance(ref.get("sha256"), str)
            or not SHA256.fullmatch(ref["sha256"])
            or type(ref.get("size")) is not int
            or not 1 <= ref["size"] <= maximum
        ):
            raise ValueError("invalid exact photo reference")
        result, size = hashlib.sha256(), 0
        with self.request("GET", ref["key"], version=ref["version"]) as response:
            if self.container == "backups" and response.headers.get("Last-Modified") != ref["stored_at"]:
                raise ValueError("Azure Blob storage time does not match the exact recovery version")
            content_type = response.headers.get("Content-Type", "").split(";", 1)[0].lower()
            if expected_type and content_type != expected_type:
                raise ValueError("Azure Blob content type does not match recovery inventory")
            while True:
                block = response.read(1024 * 1024)
                if not block:
                    break
                size += len(block)
                if size > ref["size"]:
                    raise ValueError("Azure Blob object exceeds recorded size")
                result.update(block)
                if destination is not None:
                    destination.write(block)
        if size != ref["size"] or result.hexdigest() != ref["sha256"]:
            raise ValueError("Azure Blob object integrity verification failed")

    def audit_photo(self, asset):
        if self.container != "photos":
            raise ValueError("photo audit requires the photos container")
        ref = {"key": asset["object_key"], "version": asset["version_id"], "sha256": asset["content_sha256"], "size": asset["byte_length"]}
        self.download(ref, None, asset["content_type"])

    def current_server_time(self, ref):
        if self.container != "backups":
            raise ValueError("trusted recovery time requires the backups container")
        validate_ref(ref, ref["key"].rsplit("/", 1)[-1], MAX_CIPHERTEXT)
        with self.request("HEAD", ref["key"], version=ref["version"]) as response:
            if response.headers.get("x-ms-version-id") != ref["version"] or response.headers.get("Last-Modified") != ref["stored_at"]:
                raise ValueError("Azure Blob trusted time does not match the exact recovery version")
            server_now = response.headers.get("Date")
        storage_time(server_now)
        return server_now


class MACSink:
    def __init__(self, secret, descriptor):
        self.binding = descriptor_digest(descriptor)
        self.value = hmac.new(secret, PAIR_MAGIC + self.binding, hashlib.sha256)

    def write(self, data):
        self.value.update(data)
        return len(data)


def verify_remote_pair(backup, refs, secret, descriptor):
    if len(refs) != 2:
        raise ValueError("logical backup requires an exact ciphertext pair")
    sink, sidecar = MACSink(secret, descriptor), io.BytesIO()
    backup.download(refs[0], sink)
    backup.download(refs[1], sidecar)
    if not hmac.compare_digest(sidecar.getvalue(), PAIR_MAGIC + sink.binding + sink.value.digest()):
        raise ValueError("remote logical backup authentication failed")


def publish(backup, photos, ciphertext, secret, descriptor):
    descriptor = validate_descriptor(descriptor)
    verify_pair(ciphertext, secret, descriptor)  # No network call before local authentication.
    storage_source(descriptor["environment"], backup.source["account"], backup.source["resource_id"], "backups")
    if backup.container != "backups" or photos.container != "photos" or photos.source != {key: descriptor["photo_source"][key] for key in ("account", "container", "resource_id")}:
        raise ValueError("logical recovery clients do not match their bound storage sources")
    required = [asset for asset in descriptor["assets"] if asset["required"]]
    for asset in required:
        photos.audit_photo(asset)
    prefix = f"logical-components/{descriptor['environment']}/{descriptor['generation']}/{descriptor['created']}-{descriptor['export_id']}/"
    refs = []
    for source, name in ((ciphertext, "recovery.tar.enc"), (Path(str(ciphertext) + ".mac"), "recovery.tar.enc.mac")):
        with source.open("rb") as content:
            refs.append(backup.put(prefix + name, content, source.stat().st_size, file_digest(source).hex(), require_new=True))
    verify_remote_pair(backup, refs, secret, descriptor)
    validate_completion_start(refs, backup.current_server_time(refs[-1]))
    payload = {
        "format": 1, "environment": descriptor["environment"], "generation": descriptor["generation"],
        "created": descriptor["created"], "export_id": descriptor["export_id"],
        "release_artifact": descriptor["release_artifact"], "logical_schema": descriptor["logical_schema"],
        "catalog_sha256": descriptor["catalog_sha256"], "root_sha256": descriptor["root_sha256"],
        "package_sha256": descriptor["package_sha256"], "package_bytes": descriptor["package_bytes"],
        "encryption": ENCRYPTION,
        "backup_source": backup.source, "photo_source": descriptor["photo_source"],
        "asset_count": len(descriptor["assets"]), "required_asset_count": len(required),
        "asset_root_sha256": asset_root(descriptor["assets"]), "files": refs,
    }
    marker = seal_completion(payload, secret)
    key = f"logical-completions/{descriptor['environment']}/{descriptor['generation']}/{descriptor['created']}-{descriptor['export_id']}/completion.json"
    marker_ref = backup.put(key, io.BytesIO(marker), len(marker), hashlib.sha256(marker).hexdigest(), "application/json")
    validate_publication_span(refs, marker_ref)
    return marker_ref


def inspect_private_restore(folder):
    try:
        status = folder.lstat()
        parent = folder.parent.lstat()
    except OSError as error:
        raise ValueError("logical restore folder must exist") from error
    if not stat.S_ISDIR(status.st_mode) or stat.S_ISLNK(status.st_mode) or status.st_uid != os.geteuid() or status.st_mode & 0o077:
        raise ValueError("logical restore folder must be a private owner directory")
    if not stat.S_ISDIR(parent.st_mode) or stat.S_ISLNK(parent.st_mode) or parent.st_uid != os.geteuid() or parent.st_mode & 0o077:
        raise ValueError("logical restore parent must be a private owner directory")
    entries = {item.name: item for item in folder.iterdir()}
    if not entries:
        return "empty"
    expected = {"sideyard-recovery.tar.enc", "sideyard-recovery.tar.enc.mac"}
    if set(entries) != expected:
        raise ValueError("logical restore folder has unexpected contents")
    for item in entries.values():
        item_status = item.lstat()
        if not stat.S_ISREG(item_status.st_mode) or stat.S_ISLNK(item_status.st_mode) or item_status.st_uid != os.geteuid() or item_status.st_mode & 0o077:
            raise ValueError("logical restore output is not a private owner file")
    return "complete"


def clear_private_staging(folder):
    if not folder.exists():
        return
    status = folder.lstat()
    if not stat.S_ISDIR(status.st_mode) or stat.S_ISLNK(status.st_mode) or status.st_uid != os.geteuid() or status.st_mode & 0o077:
        raise ValueError("unsafe logical restore staging path")
    allowed = {".recovery.partial", ".recovery.mac.partial", "sideyard-recovery.tar.enc", "sideyard-recovery.tar.enc.mac"}
    for item in folder.iterdir():
        item_status = item.lstat()
        if item.name not in allowed or not stat.S_ISREG(item_status.st_mode) or stat.S_ISLNK(item_status.st_mode) or item_status.st_uid != os.geteuid():
            raise ValueError("unsafe logical restore staging contents")
        item.unlink()
    folder.rmdir()


def private_output(path):
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
    if hasattr(os, "O_NOFOLLOW"):
        flags |= os.O_NOFOLLOW
    return os.fdopen(os.open(path, flags, 0o600), "wb")


def verify_local_ref(path, ref):
    if path.stat().st_size != ref["size"] or file_digest(path).hex() != ref["sha256"]:
        raise ValueError("existing logical restore output does not match its exact Blob version")


def retrieve(backup, marker_ref, folder, secret, expected):
    match = validate_ref(marker_ref, "completion.json", MAX_COMPLETION)
    descriptor = validate_descriptor(expected)
    if backup.container != "backups":
        raise ValueError("logical restore requires the backups container")
    storage_source(descriptor["environment"], backup.source["account"], backup.source["resource_id"], "backups")
    identity = (descriptor["environment"], descriptor["generation"], descriptor["created"], descriptor["export_id"])
    if ref_identity(match) != identity:
        raise ValueError("logical backup marker reference does not match the expected recovery")
    restore_state = inspect_private_restore(folder)
    marker = io.BytesIO()
    backup.download(marker_ref, marker)
    required = [asset for asset in descriptor["assets"] if asset["required"]]
    binding = dict(descriptor)
    binding.update({
        "asset_count": len(descriptor["assets"]),
        "required_asset_count": len(required),
        "asset_root_sha256": asset_root(descriptor["assets"]),
    })
    payload = open_completion(marker.getvalue(), secret, binding, backup)
    validate_publication_span(payload["files"], marker_ref)
    if ref_identity(match) != (payload["environment"], payload["generation"], payload["created"], payload["export_id"]):
        raise ValueError("logical backup marker reference path mismatch")
    existing = folder / "sideyard-recovery.tar.enc"
    existing_sidecar = Path(str(existing) + ".mac")
    if restore_state == "complete":
        verify_local_ref(existing, payload["files"][0])
        verify_local_ref(existing_sidecar, payload["files"][1])
        if not hmac.compare_digest(existing_sidecar.read_bytes(), PAIR_MAGIC + pair_digest(existing, secret, descriptor)):
            raise ValueError("existing logical restore output authentication failed")
        return existing
    staging = folder.parent / f".{folder.name}.sideyard-{marker_ref['sha256'][:16]}.partial"
    clear_private_staging(staging)
    staging.mkdir(mode=0o700)
    destination = staging / "sideyard-recovery.tar.enc"
    sidecar = staging / "sideyard-recovery.tar.enc.mac"
    partials = [staging / ".recovery.partial", staging / ".recovery.mac.partial"]
    promoted = False
    try:
        for ref, target in zip(payload["files"], partials):
            with private_output(target) as output:
                backup.download(ref, output)
                output.flush()
                os.fsync(output.fileno())
        if not hmac.compare_digest(partials[1].read_bytes(), PAIR_MAGIC + pair_digest(partials[0], secret, descriptor)):
            raise ValueError("downloaded logical backup authentication failed")
        partials[1].rename(sidecar)
        partials[0].rename(destination)
        directory_fd = os.open(staging, os.O_RDONLY)
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)
        os.replace(staging, folder)
        promoted = True
        parent_fd = os.open(folder.parent, os.O_RDONLY)
        try:
            os.fsync(parent_fd)
        finally:
            os.close(parent_fd)
    finally:
        if not promoted:
            clear_private_staging(staging)
    return folder / "sideyard-recovery.tar.enc"
