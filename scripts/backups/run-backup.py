#!/usr/bin/env python3
"""Receive authenticated Sideyard sets on G5, optionally verify them on Drive."""
import argparse
import configparser
import datetime
import fcntl
import hashlib
import hmac
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import time
from archive import ARCHIVE_ID, DOMAIN, FILES, MAX_ARCHIVE_BYTES, atomic_json, canonical, private_directory, settings, verify

FOLDER_ID = '1CZlgUMeimavN-IyLKZ2Do9T2IaHjFkxL'
CONFIG_ROOT = Path('/etc/sideyard-backup')
ARCHIVE_ROOT = Path('/var/backups/sideyard-pg')
STATE_ROOT = Path('/var/lib/sideyard-backup')


def receive(transport, directory):
    names = set()
    with tarfile.open(fileobj=transport, mode='r|') as source:
        for entry in source:
            if entry.name not in FILES or entry.name in names or not entry.isfile() or entry.size < 1:
                raise ValueError('Unexpected encrypted transport member')
            maximum = MAX_ARCHIVE_BYTES if entry.name == FILES[0] else (39 if entry.name == FILES[1] else 8192)
            if entry.size > maximum:
                raise ValueError('Encrypted transport exceeds its limit')
            names.add(entry.name)
            with source.extractfile(entry) as incoming, (directory/entry.name).open('xb') as out:
                os.chmod(directory/entry.name, 0o600)
                shutil.copyfileobj(incoming, out, 1024**2)
                out.flush(); os.fsync(out.fileno())
    if names != set(FILES):
        raise ValueError('Incomplete encrypted transport')


def upload_drive(folder, environment, identity):
    config = CONFIG_ROOT/'rclone.conf'
    # Check protected ownership and pin the existing dedicated child ID.
    settings_file = config.lstat()
    if config.is_symlink() or settings_file.st_uid != os.geteuid() or settings_file.st_mode & 0o077:
        raise ValueError('Unsafe Drive credential configuration')
    parsed = configparser.ConfigParser(interpolation=None)
    parsed.read(config)
    if parsed['sideyard-drive'].get('type') != 'drive' or parsed['sideyard-drive'].get('root_folder_id') != FOLDER_ID:
        raise ValueError('Drive destination does not match the dedicated folder')
    base = ['/usr/local/bin/rclone', '--config', str(config), '--contimeout', '20s', '--timeout', '90s', '--retries', '2']
    destination = f'sideyard-drive:{environment}/{identity}'
    for command in [
        ['copy', str(folder), destination, '--immutable', '--checksum', '--exclude', 'manifest.json'],
        ['check', str(folder), destination, '--download', '--one-way', '--exclude', 'manifest.json'],
        ['copy', str(folder), destination, '--immutable', '--checksum', '--include', 'manifest.json'],
        ['check', str(folder), destination, '--download', '--one-way'],
    ]:
        result = subprocess.run(base+command, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=900)
        if result.returncode:
            raise ValueError('Drive upload or independent content verification failed')


def prune_local(root, key, environment, days, now):
    cutoff = now - days*86400
    candidates = []
    for path in root.iterdir():
        if path.is_symlink() or not path.is_dir() or not ARCHIVE_ID.fullmatch(path.name):
            continue
        try:
            payload = verify(path, environment, key)
            created = datetime.datetime.fromisoformat(payload['created']).timestamp()
        except (ValueError, OSError, KeyError):
            continue  # Unknown/incomplete sets need operator inspection, not deletion.
        candidates.append((created, path))
    newest = max((date for date, _ in candidates), default=now)
    for created, path in candidates:
        if created < cutoff and created < newest:
            shutil.rmtree(path)


def prune_drive(environment, current_id, key, now):
    """Delete only expired, signed sets under the pinned Sideyard child folder."""
    base = ['/usr/local/bin/rclone', '--config', str(CONFIG_ROOT/'rclone.conf'), '--contimeout', '20s', '--timeout', '60s', '--retries', '1']
    def remote(args):
        result = subprocess.run(base+args, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=120)
        if result.returncode:
            raise ValueError('Drive retention operation failed')
        return result.stdout
    parent = f'sideyard-drive:{environment}'
    directories = json.loads(remote(['lsjson', parent, '--dirs-only']))
    names = [item['Name'] for item in directories]
    for identity in names:
        if identity == current_id or names.count(identity) != 1 or not ARCHIVE_ID.fullmatch(identity):
            continue
        target = parent+'/'+identity
        components = json.loads(remote(['lsjson', target]))
        if any(item['IsDir'] for item in components) or {item['Name'] for item in components} != set(FILES) or len(components) != 3:
            continue
        if next(item['Size'] for item in components if item['Name'] == 'manifest.json') > 8192:
            continue
        try:
            marker = json.loads(remote(['cat', target+'/manifest.json']))
            payload = marker['payload']
            expected = hmac.new(key, DOMAIN+canonical(payload), hashlib.sha256).hexdigest()
            created = datetime.datetime.fromisoformat(payload['created'])
            valid = (hmac.compare_digest(marker['hmac'], expected) and payload['format'] == 1
                     and payload['environment'] == environment and payload['id'] == identity
                     and payload['backend'] == 'postgres' and created.tzinfo is not None)
        except (ValueError, KeyError, TypeError):
            continue
        if valid and created.timestamp() < now-7*86400:
            remote(['purge', target, '--drive-use-trash=false'])


def run(environment, drive, candidate=False):
    os.umask(0o077)
    state = private_directory(STATE_ROOT)
    root = private_directory(ARCHIVE_ROOT/environment)
    config = settings(CONFIG_ROOT/'source.env')
    key = settings(CONFIG_ROOT/f'{environment}-recovery.env')['CLUBDUE_BACKUP_HMAC_KEY'].encode()
    with (state/f'{environment}.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        ssh = ['/usr/bin/ssh', '-F', '/dev/null', '-i', str(CONFIG_ROOT/'source.key'), '-o', 'BatchMode=yes', '-o', 'IdentitiesOnly=yes', '-o', 'StrictHostKeyChecking=yes', '-o', 'UserKnownHostsFile='+str(CONFIG_ROOT/'known_hosts'), '-o', 'ConnectTimeout=20', '-o', 'ServerAliveInterval=30', '-o', 'ServerAliveCountMax=2', 'root@'+config['SOURCE_HOST'], 'backup '+environment]
        with tempfile.TemporaryDirectory(prefix='.incoming-', dir=root) as temporary:
            directory = Path(temporary)
            child = subprocess.Popen(ssh, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
            try:
                receive(child.stdout, directory)
                if child.wait(timeout=30):
                    raise ValueError('Restricted source export failed')
            finally:
                child.stdout.close()
                if child.poll() is None:
                    child.kill(); child.wait()
            payload = verify(directory, environment, key)
            created = datetime.datetime.fromisoformat(payload['created']).timestamp()
            if not time.time()-3600 <= created <= time.time()+300:
                raise ValueError('Source export timestamp is not current')
            target = root/payload['id']
            if target.exists():
                raise ValueError('Backup identity collision')
            os.rename(directory, target)
            fd = os.open(root, os.O_RDONLY | os.O_DIRECTORY)
            try:
                os.fsync(fd)
            finally:
                os.close(fd)
            if drive:
                upload_drive(target, environment, payload['id'])
            now = int(time.time())
            if drive:
                prune_drive(environment, payload['id'], key, now)
            marker = {'environment': environment, 'archive': payload['id'], 'verified_at': now, 'source_revision': payload['source_revision'], 'destination': 'google-drive' if drive else 'g5', 'folder_id': FOLDER_ID if drive else None}
            marker_name = f'{environment}.drive-candidate.json' if candidate else f'{environment}.last-success.json'
            atomic_json(state/marker_name, marker)
            # Prune only after the selected off-host destination verifies.
            prune_local(root, key, environment, 7, now)
            print(json.dumps(marker))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('environment', choices=['production', 'staging'])
    parser.add_argument('--drive', action='store_true')
    parser.add_argument('--candidate', action='store_true', help='Verify Drive without replacing scheduled backup success')
    args = parser.parse_args()
    try:
        if args.candidate and not args.drive:
            raise ValueError('Drive candidate verification requires --drive')
        drive = args.drive or settings(CONFIG_ROOT/'source.env').get('DESTINATION') == 'google-drive'
        run(args.environment, drive, args.candidate)
    except (ValueError, OSError, KeyError, subprocess.SubprocessError, tarfile.TarError):
        raise SystemExit('Sideyard backup failed; previous success and verified sets were preserved')
