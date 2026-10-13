"""Sideyard encrypted archive contract; no cloud credentials or database access."""
import hashlib
import hmac
import json
import os
from pathlib import Path
import re
import shlex
import stat

MAGIC = b'SYMAC2\n'
DOMAIN = b'Sideyard backup set v1\n'
FILES = ('database.sql.enc', 'database.sql.enc.mac', 'manifest.json')
ARCHIVE_ID = re.compile(r'\d{8}T\d{6}Z-[0-9a-f]{32}')
MAX_ARCHIVE_BYTES = 8 * 1024**3


def private_directory(path):
    path = Path(path)
    if path.is_symlink():
        raise ValueError('Unsafe private directory')
    path.mkdir(mode=0o700, parents=True, exist_ok=True)
    st = path.stat()
    if not stat.S_ISDIR(st.st_mode) or st.st_uid != os.geteuid() or st.st_mode & 0o077:
        raise ValueError('Private directory must be owner-only')
    return path


def settings(path):
    path = Path(path)
    st = path.lstat()
    if not stat.S_ISREG(st.st_mode) or st.st_uid != os.geteuid() or st.st_mode & 0o077:
        raise ValueError('Unsafe protected configuration')
    result = {}
    for line in path.read_text().splitlines():
        if not line.strip() or line.lstrip().startswith('#'):
            continue
        name, equal, value = line.partition('=')
        if not equal or not re.fullmatch('[A-Z_]+', name):
            raise ValueError('Invalid protected configuration')
        parts = shlex.split(value)
        if len(parts) != 1 or name in result:
            raise ValueError('Invalid protected configuration')
        result[name] = parts[0]
    return result


def file_digest(path, key=None):
    value = hashlib.sha256() if key is None else hmac.new(key, MAGIC, hashlib.sha256)
    with Path(path).open('rb') as f:
        for chunk in iter(lambda: f.read(1024**2), b''):
            value.update(chunk)
    return value.digest()


def canonical(payload):
    return json.dumps(payload, sort_keys=True, separators=(',', ':')).encode()


def manifest(payload, key):
    return canonical({'payload': payload, 'hmac': hmac.new(key, DOMAIN + canonical(payload), hashlib.sha256).hexdigest()})


def verify(folder, environment, key):
    folder = Path(folder)
    if set(p.name for p in folder.iterdir()) != set(FILES):
        raise ValueError('Incomplete or unexpected backup set')
    for name in FILES:
        p = folder / name
        if p.is_symlink() or not p.is_file() or p.stat().st_nlink != 1:
            raise ValueError('Unsafe backup component')
    ciphertext = folder / FILES[0]
    if not key or (folder / FILES[1]).stat().st_size != 39:
        raise ValueError('Backup authentication key or sidecar is invalid')
    mac = (folder / FILES[1]).read_bytes()
    if not 1 <= ciphertext.stat().st_size <= MAX_ARCHIVE_BYTES or not hmac.compare_digest(mac, MAGIC + file_digest(ciphertext, key)):
        raise ValueError('Backup authentication failed')
    marker = folder / FILES[2]
    if marker.stat().st_size > 8192:
        raise ValueError('Backup manifest too large')
    item = json.loads(marker.read_bytes())
    payload = item['payload']
    expected = hmac.new(key, DOMAIN + canonical(payload), hashlib.sha256).hexdigest()
    if not hmac.compare_digest(item['hmac'], expected):
        raise ValueError('Backup manifest authentication failed')
    if payload['format'] != 1 or payload['environment'] != environment or payload['backend'] != 'postgres' or not ARCHIVE_ID.fullmatch(payload['id']):
        raise ValueError('Backup manifest scope mismatch')
    if not re.fullmatch('[0-9a-f]{40}', payload['source_revision']) or not payload.get('data_generation'):
        raise ValueError('Backup source revision missing')
    for name in FILES[:2]:
        info = payload['files'][name]
        if info['bytes'] != (folder/name).stat().st_size or info['sha256'] != file_digest(folder/name).hex():
            raise ValueError('Backup component checksum mismatch')
    return payload


def atomic_json(path, value):
    import tempfile
    path = Path(path)
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as f:
        staged = Path(f.name)
        os.chmod(staged, 0o600)
        f.write(canonical(value)); f.flush(); os.fsync(f.fileno())
    try:
        os.replace(staged, path)
        fd = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)
    finally:
        staged.unlink(missing_ok=True)
