#!/usr/bin/env python3
"""Publish/retrieve authenticated encrypted PostgreSQL exports in private Blob.

Python stdlib only. No plaintext database data, credentials in command arguments,
account keys, public URLs, or photo garbage collection. Uploads are create-only.
"""
import argparse
from email.utils import formatdate
import hashlib
import hmac
import io
import json
import os
from pathlib import Path
import re
import sys
import urllib.error
import urllib.parse
import urllib.request

MAX_BACKUP = 256 * 1024 * 1024
MAGIC = b"SYMAC2\n"
NAME = re.compile(r"clubdue-postgres-(\d{8}T\d{6}Z)\.sql\.enc")
KEY = re.compile(r"postgres/\d{8}T\d{6}Z/(database\.sql\.enc(?:\.mac)?|manifest\.json)")


def file_hash(path, key=None):
    result = hashlib.sha256() if key is None else hmac.new(key, MAGIC, hashlib.sha256)
    with path.open("rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            result.update(block)
    return result.digest()


def verify_pair(path, secret):
    if not secret or not 1 <= path.stat().st_size <= MAX_BACKUP:
        raise ValueError("backup secret and a bounded encrypted backup are required")
    sidecar = Path(str(path) + ".mac")
    if sidecar.stat().st_size != 39:
        raise ValueError("backup authentication sidecar must use the 39-byte versioned format")
    actual = sidecar.read_bytes()
    if not hmac.compare_digest(actual, MAGIC + file_hash(path, secret)):
        raise ValueError("backup authentication failed before upload/restore")


def seal_manifest(payload, secret):
    data = json.dumps(payload, sort_keys=True, separators=(",", ":")).encode()
    return json.dumps({"payload": payload, "hmac": hmac.new(secret, b"Sideyard backup manifest v1\n" + data, hashlib.sha256).hexdigest()}, sort_keys=True).encode()


def open_manifest(data, secret, account):
    if not secret or len(data) > 16384:
        raise ValueError("invalid backup manifest")
    doc = json.loads(data)
    payload = doc["payload"]
    expected = json.loads(seal_manifest(payload, secret))["hmac"]
    if not hmac.compare_digest(doc["hmac"], expected):
        raise ValueError("backup manifest authentication failed")
    if payload["format"] != 1 or payload["account"] != account or payload["container"] != "backups" or payload["recovery"] != "inline-postgres-photos":
        raise ValueError("backup manifest belongs to another account or format")
    if not re.fullmatch(r"\d{8}T\d{6}Z", payload["created"]):
        raise ValueError("invalid backup timestamp")
    refs = payload["files"]
    if len(refs) != 2 or [r["key"].split("/")[-1] for r in refs] != ["database.sql.enc", "database.sql.enc.mac"]:
        raise ValueError("backup manifest must contain its database and authentication sidecar")
    for ref in refs:
        if not KEY.fullmatch(ref["key"]) or not ref["version"] or not re.fullmatch(r"[a-f0-9]{64}", ref["sha256"]) or not 1 <= ref["size"] <= MAX_BACKUP:
            raise ValueError("invalid backup object reference")
        if ref["key"].split("/")[1] != payload["created"]:
            raise ValueError("backup timestamp mismatch")
    if refs[1]["size"] != 39:
        raise ValueError("invalid backup authentication sidecar size")
    if refs[0]["key"] + ".mac" != refs[1]["key"]:
        raise ValueError("backup reference pair mismatch")
    return payload


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


class Blob:
    def __init__(self, account, token, opener):
        if not re.fullmatch(r"sideyard[a-z0-9]{1,16}", account):
            raise ValueError("expected a Sideyard Azure storage account")
        self.account, self.token, self.opener = account, token, opener

    def request(self, method, key, version=None, data=None, size=0):
        if not KEY.fullmatch(key):
            raise ValueError("invalid backup object name")
        url = f"https://{self.account}.blob.core.windows.net/backups/{key}"
        if version:
            url += "?" + urllib.parse.urlencode({"versionid": version})
        headers = {"Authorization": "Bearer " + self.token, "x-ms-version": "2023-11-03", "x-ms-date": formatdate(usegmt=True)}
        if method == "PUT":
            headers.update({"x-ms-blob-type": "BlockBlob", "If-None-Match": "*", "Content-Length": str(size), "Content-Type": "application/octet-stream"})
        return self.opener.open(urllib.request.Request(url, data=data, method=method, headers=headers), timeout=120)

    def put(self, key, source, size, digest):
        try:
            with self.request("PUT", key, data=source, size=size) as response:
                version = response.headers.get("x-ms-version-id")
        except urllib.error.HTTPError as error:
            if error.code not in (409, 412):
                raise ValueError(f"Azure backup upload failed (HTTP {error.code})") from None
            error.close()
            with self.request("HEAD", key) as response:
                version = response.headers.get("x-ms-version-id")
        if not version:
            raise ValueError("backup storage versioning must be enabled")
        ref = {"key": key, "version": version, "sha256": digest, "size": size}
        # Read back the exact remote version before publishing a completion marker.
        self.download(ref, None)
        return ref

    def download(self, ref, destination):
        result, size = hashlib.sha256(), 0
        with self.request("GET", ref["key"], version=ref["version"]) as response:
            while True:
                block = response.read(1024 * 1024)
                if not block:
                    break
                size += len(block)
                if size > ref["size"]:
                    raise ValueError("backup object exceeds recorded size")
                result.update(block)
                if destination:
                    destination.write(block)
        if size != ref["size"] or result.hexdigest() != ref["sha256"]:
            raise ValueError("backup object integrity verification failed")


def publish(blob, path, secret):
    match = NAME.fullmatch(path.name)
    if not match:
        raise ValueError("expected a timestamped encrypted PostgreSQL backup")
    verify_pair(path, secret)
    prefix = "postgres/" + match[1] + "/"
    refs = []
    for source, name in [(path, "database.sql.enc"), (Path(str(path) + ".mac"), "database.sql.enc.mac")]:
        with source.open("rb") as content:
            refs.append(blob.put(prefix + name, content, source.stat().st_size, file_hash(source).hex()))
    payload = {"format": 1, "account": blob.account, "container": "backups", "created": match[1], "recovery": "inline-postgres-photos", "files": refs}
    marker = seal_manifest(payload, secret)
    key = prefix + "manifest.json"
    blob.put(key, io.BytesIO(marker), len(marker), hashlib.sha256(marker).hexdigest())
    return key


def retrieve(blob, key, folder, secret):
    if not KEY.fullmatch(key) or not key.endswith("/manifest.json"):
        raise ValueError("expected an exact backup manifest key")
    if not folder.is_dir() or any(folder.iterdir()):
        raise ValueError("restore download requires an existing empty directory")
    with blob.request("GET", key) as response:
        manifest = response.read(16385)
    payload = open_manifest(manifest, secret, blob.account)
    if key != payload["files"][0]["key"].rsplit("/", 1)[0] + "/manifest.json":
        raise ValueError("backup manifest path mismatch")
    path = folder / ("clubdue-postgres-" + payload["created"] + ".sql.enc")
    if not NAME.fullmatch(path.name):
        raise ValueError("invalid backup timestamp")
    # Only completed, verified downloads become visible under restore filenames.
    temporary = [folder / ".database.partial", folder / ".mac.partial"]
    try:
        for ref, target in zip(payload["files"], temporary):
            with target.open("xb") as output:
                blob.download(ref, output)
        if not hmac.compare_digest(temporary[1].read_bytes(), MAGIC + file_hash(temporary[0], secret)):
            raise ValueError("downloaded backup authentication failed")
        temporary[1].rename(str(path) + ".mac")
        temporary[0].rename(path)
    finally:
        for item in temporary:
            item.unlink(missing_ok=True)
    return path


def connect(env):
    account = env.get("AZURE_BACKUP_ACCOUNT", "")
    tenant, client, secret = (env.get(key, "") for key in ("AZURE_TENANT_ID", "AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET"))
    if not all(re.fullmatch(r"[a-fA-F0-9-]{36}", value) for value in (tenant, client)) or not secret:
        raise ValueError("backup service principal credentials are required")
    opener = urllib.request.build_opener(NoRedirect())
    body = urllib.parse.urlencode({"grant_type": "client_credentials", "client_id": client, "client_secret": secret, "scope": "https://storage.azure.com/.default"}).encode()
    request = urllib.request.Request(f"https://login.microsoftonline.com/{tenant}/oauth2/v2.0/token", data=body)
    with opener.open(request, timeout=30) as response:
        token = json.loads(response.read(65536))["access_token"]
    return Blob(account, token, opener)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["upload", "download"])
    parser.add_argument("source")
    parser.add_argument("destination", nargs="?")
    args = parser.parse_args()
    os.umask(0o077)
    secret = os.environ.get("CLUBDUE_BACKUP_HMAC_KEY", "").encode()
    if not secret:
        raise ValueError("CLUBDUE_BACKUP_HMAC_KEY required")
    if args.command == "upload":
        verify_pair(Path(args.source), secret)  # Reject untrusted local data before any network call.
    blob = connect(os.environ)
    if args.command == "upload":
        print(publish(blob, Path(args.source), secret))
    else:
        if not args.destination:
            raise ValueError("download requires an empty destination directory")
        print(retrieve(blob, args.source, Path(args.destination), secret))


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, KeyError, TypeError) as error:
        # HTTP bodies/URLs may contain sensitive provider details; don't print them.
        if isinstance(error, urllib.error.HTTPError):
            print(f"Azure backup operation failed (HTTP {error.code})", file=sys.stderr)
        elif isinstance(error, ValueError):
            print(str(error), file=sys.stderr)
        else:
            print("Azure backup operation failed; inspect protected configuration and retry", file=sys.stderr)
        sys.exit(65)
