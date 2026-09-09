#!/usr/bin/env python3
"""Authenticate encrypted backup files without putting keys in process arguments."""
import hashlib
import hmac
import os
from pathlib import Path
import sys

MAGIC = b"SYMAC2\n"


def digest(path, key, prefix=MAGIC):
    mac = hmac.new(key, prefix, hashlib.sha256)
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            mac.update(chunk)
    return mac.digest()


def main(args):
    if len(args) != 2 or args[0] not in ("sign", "verify", "verify-legacy"):
        raise ValueError("usage: backup-auth.py sign|verify|verify-legacy encrypted-file")
    mode, name = args
    path = Path(name)
    if mode == "verify-legacy":
        # Historical OpenSSL syntax used this literal string instead of reading
        # fd 3. This is a public checksum, NOT proof that a backup is authentic.
        expected = digest(path, b"file:/dev/fd/3", b"")
        actual = Path(name + ".mac").read_bytes()
        if len(actual) != 32 or not hmac.compare_digest(actual, expected):
            raise ValueError("legacy backup checksum failed")
        print("WARNING: legacy backup has no trustworthy authentication; isolated recovery only", file=sys.stderr)
        return
    key = os.environ.get("CLUBDUE_BACKUP_HMAC_KEY", "").encode()
    if not key:
        raise ValueError("CLUBDUE_BACKUP_HMAC_KEY required")
    expected = MAGIC + digest(path, key)
    if mode == "sign":
        sys.stdout.buffer.write(expected)
        return
    actual = Path(name + ".mac").read_bytes()
    if len(actual) == 32:
        raise ValueError("legacy backup sidecar requires explicit isolated recovery; see docs/DEPLOY.md")
    if not hmac.compare_digest(actual, expected):
        raise ValueError("backup authentication failed")


if __name__ == "__main__":
    try:
        main(sys.argv[1:])
    except (ValueError, OSError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(65)
