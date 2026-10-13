#!/usr/bin/env python3
"""Fixed-command Sideyard exporter: stdout contains encrypted files only."""
import datetime
import fcntl
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import uuid
import resource
import urllib.parse
from archive import FILES, MAGIC, MAX_ARCHIVE_BYTES, file_digest, manifest, private_directory, settings


def export(environment, output):
    if environment not in ('production', 'staging'):
        raise ValueError('Unsupported backup environment')
    config = settings(f'/etc/sideyard-backup/{environment}-recovery.env')
    key = config['CLUBDUE_BACKUP_HMAC_KEY'].encode()
    app = 'clubdue-current' if environment == 'production' else 'clubdue-staging-current'
    # Match the deployment/backend-operation lock before reading source identity.
    locks = private_directory('/run/sideyard-locks')
    with (locks/f'sideyard-{environment}-database-window.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if not key or not config.get('CLUBDUE_BACKUP_PASSPHRASE'):
            raise ValueError('Backup keys must not be empty')
        item = json.loads(subprocess.run(['docker', 'inspect', app], check=True, capture_output=True).stdout)[0]
        env = dict(e.split('=', 1) for e in item['Config']['Env'] if '=' in e)
        labels = item['Config']['Labels']
        state_file = Path('/var/lib/sideyard-operations')/f'{environment}-backend.env'
        if state_file.exists():
            state = settings(state_file)
            if state.get('DATABASE_BACKEND') != 'postgres' or labels.get('sideyard.backend') != 'postgres' or labels.get('sideyard.generation') != state.get('DATA_GENERATION'):
                raise ValueError('Application does not match authoritative PostgreSQL generation')
            generation = state['DATA_GENERATION']
        elif environment != 'production' or any(name.startswith('sideyard.') for name in labels):
            raise ValueError('Sideyard runtime state is missing')
        else:
            # Pre-generation production release: bind the fixed database container.
            generation = None
        if not item['State']['Running'] or env.get('DATABASE_BACKEND', 'postgres') != 'postgres':
            raise ValueError('Current application is not backed by PostgreSQL')
        dsn = urllib.parse.urlparse(env.get('DATABASE_URL', ''))
        database = 'clubdue-'+environment+'-db'
        db = json.loads(subprocess.run(['docker', 'inspect', database], check=True, capture_output=True).stdout)[0]
        aliases = set()
        for name, network in db['NetworkSettings']['Networks'].items():
            if name in item['NetworkSettings']['Networks']:
                aliases.update(network.get('Aliases') or [])
                aliases.update(network.get('DNSNames') or [])
                aliases.add(db['Name'].lstrip('/'))
        if dsn.scheme not in ('postgres', 'postgresql') or dsn.hostname not in aliases or dsn.path != '/clubdue' or dsn.username != 'clubdue' or not db['State']['Running']:
            raise ValueError('Application connection does not identify the fixed source database')
        if generation is None:
            generation = 'legacy-postgres-container:' + db['Id']
        revision = labels.get('org.opencontainers.image.revision', '')
        now = datetime.datetime.now(datetime.timezone.utc)
        identity = now.strftime('%Y%m%dT%H%M%SZ') + '-' + uuid.uuid4().hex
        os.umask(0o077)
        spool = private_directory('/var/backups/sideyard-export-temp')
        space = os.statvfs(spool)
        limit = min(MAX_ARCHIVE_BYTES, space.f_bavail*space.f_frsize//4)
        if limit < 32*1024**2:
            raise ValueError('Insufficient source backup space')
        def bound_output():
            resource.setrlimit(resource.RLIMIT_FSIZE, (limit, limit))
        with tempfile.TemporaryDirectory(prefix='sideyard-export-', dir=spool) as work:
            folder = Path(work)
            cipher = folder/FILES[0]
            with cipher.open('wb') as encrypted:
                dump = subprocess.Popen(['docker', 'exec', 'clubdue-'+environment+'-db', 'pg_dump', '--lock-wait-timeout=30s', '--no-password', '--no-owner', '--no-acl', '--username=clubdue', '--dbname=clubdue', '--format=plain'], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
                try:
                    crypt = subprocess.run(['openssl', 'enc', '-aes-256-cbc', '-salt', '-pbkdf2', '-pass', 'env:CLUBDUE_BACKUP_PASSPHRASE'], stdin=dump.stdout, stdout=encrypted, stderr=subprocess.DEVNULL, env={**os.environ, 'CLUBDUE_BACKUP_PASSPHRASE': config['CLUBDUE_BACKUP_PASSPHRASE']}, timeout=900, preexec_fn=bound_output)
                    dump.stdout.close()
                    if dump.wait() or crypt.returncode:
                        raise ValueError('Sideyard database export failed')
                finally:
                    if dump.poll() is None:
                        dump.kill(); dump.wait()
            (folder/FILES[1]).write_bytes(MAGIC + file_digest(cipher, key))
            payload = {'format': 1, 'environment': environment, 'backend': 'postgres', 'id': identity, 'created': now.isoformat(), 'source_revision': revision, 'data_generation': generation,
                       'files': {name: {'bytes': (folder/name).stat().st_size, 'sha256': file_digest(folder/name).hex()} for name in FILES[:2]}}
            (folder/FILES[2]).write_bytes(manifest(payload, key))
            with tarfile.open(fileobj=output, mode='w|') as archive:
                for name in FILES:
                    archive.add(folder/name, arcname=name, recursive=False)


if __name__ == '__main__':
    try:
        command = os.environ.get('SSH_ORIGINAL_COMMAND', '').split()
        if len(command) != 2 or command[1] not in ('production', 'staging') or command[0] not in ('backup', 'receipt'):
            raise ValueError('Only fixed backup or receipt operations are allowed')
        if command[0] == 'backup':
            export(command[1], sys.stdout.buffer)
        else:
            import importlib.util
            spec = importlib.util.spec_from_file_location('receipt', Path(__file__).with_name('record-receipt.py'))
            receipt = importlib.util.module_from_spec(spec)
            spec.loader.exec_module(receipt)
            receipt.record(command[1], sys.stdin.buffer)
    except (ValueError, OSError, KeyError, subprocess.SubprocessError):
        sys.exit('Sideyard encrypted export failed; inspect protected source configuration')
