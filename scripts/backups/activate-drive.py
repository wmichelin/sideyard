#!/usr/bin/env python3
"""After Google sign-in, verify Drive safeguards and switch the installed jobs."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import fcntl
import uuid
from archive import atomic_json, private_directory, settings, verify

ROOT=Path('/etc/sideyard-backup')
STATE=Path('/var/lib/sideyard-backup')
FOLDER='1CZlgUMeimavN-IyLKZ2Do9T2IaHjFkxL'


def restore_candidate(environment, candidate, base):
    from importlib.util import spec_from_file_location, module_from_spec
    spec=spec_from_file_location('recovery',Path(__file__).with_name('verify-backup.py'))
    recovery=module_from_spec(spec);spec.loader.exec_module(recovery)
    container='sideyard-recovery-drive-'+uuid.uuid4().hex[:12]
    with tempfile.TemporaryDirectory(prefix='.drive-restore-',dir=STATE) as temporary:
        folder=Path(temporary)
        subprocess.run(base+['copy',f"sideyard-drive:{environment}/{candidate['archive']}",str(folder),'--immutable','--checksum'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=900)
        try:
            subprocess.run(['docker','run','-d','--name',container,'--network','none','--tmpfs','/var/lib/postgresql/data:rw,nosuid,size=512m','-e','POSTGRES_HOST_AUTH_METHOD=trust','-e','POSTGRES_USER=clubdue','-e','POSTGRES_DB=clubdue','postgres:16-alpine'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
            for _ in range(120):
                logs=subprocess.run(['docker','logs',container],capture_output=True,check=True).stdout
                ready=subprocess.run(['docker','exec',container,'pg_isready','-U','clubdue'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode==0
                if b'PostgreSQL init process complete; ready for start up.' in logs and ready:break
                time.sleep(.5)
            else:raise ValueError('Isolated Drive recovery database did not start')
            receipt=recovery.check(environment,folder,container)
            if receipt['archive'] != candidate['archive'] or receipt['source_revision'] != candidate['source_revision']:
                raise ValueError('Downloaded Drive recovery does not match the verified candidate')
            receipt['verified_at']=int(time.time())
            atomic_json(STATE/f'{environment}.last-drive-restore.json',receipt)
        finally:
            subprocess.run(['docker','rm','-f',container],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)


def activate():
    os.umask(0o077)
    state=private_directory(STATE)
    if (state/'drive-activated.json').exists():
        return
    import configparser
    config = ROOT/'rclone.conf'
    if config.is_symlink() or config.stat().st_uid != os.geteuid() or config.stat().st_mode & 0o077:
        raise ValueError('Unsafe Drive configuration')
    parsed=configparser.ConfigParser(interpolation=None);parsed.read(config)
    if parsed['sideyard-drive'].get('type') != 'drive' or parsed['sideyard-drive'].get('root_folder_id')!=FOLDER:
        raise ValueError('Unexpected Drive folder; backup destination unchanged')
    base=['/usr/local/bin/rclone','--config',str(ROOT/'rclone.conf'),'--contimeout','20s','--timeout','60s','--retries','1']
    def run(args, expected=True):
        result=subprocess.run(base+args,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,timeout=120)
        if expected and result.returncode:raise ValueError('Drive acceptance probe failed')
        return result
    quota=json.loads(run(['about','sideyard-drive:','--json']).stdout)
    if 'free' not in quota or quota['free'] < 100*1024**2:
        raise ValueError('Drive quota cannot be verified or has less than 100 MB free')
    # Validate real-backend identical retry and refusal to overwrite changed bytes.
    probe='sideyard-drive:acceptance-'+uuid.uuid4().hex
    with tempfile.TemporaryDirectory() as temporary:
        folder=Path(temporary);file=folder/'synthetic.txt';file.write_bytes(b'Original synthetic backup probe\n')
        try:
            for _ in range(2):run(['copy',str(folder),probe,'--immutable','--checksum'])
            run(['check',str(folder),probe,'--download','--one-way'])
            file.write_bytes(b'Changed synthetic backup probe!\n')
            if run(['copy',str(folder),probe,'--immutable','--checksum'],expected=False).returncode==0:
                raise ValueError('Drive did not refuse an immutable content collision')
            if run(['cat',probe+'/synthetic.txt']).stdout!=b'Original synthetic backup probe\n':
                raise ValueError('Drive changed existing immutable content')
        finally:
            run(['purge',probe,'--drive-use-trash=false'])
    # Explicit candidates do not alter the currently scheduled G5 mode.
    started = int(time.time())
    candidates = {}
    for environment in ('staging','production'):
        subprocess.run(['/usr/bin/python3', '/opt/sideyard-backup/run-backup.py', environment, '--drive', '--candidate'], check=True, stdout=subprocess.DEVNULL)
        candidate = json.loads((state/f'{environment}.drive-candidate.json').read_text())
        if candidate.get('destination') != 'google-drive' or candidate.get('folder_id') != FOLDER or candidate.get('verified_at', 0) < started or candidate.get('environment') != environment:
            raise ValueError('Initial Drive backup did not verify')
        restore_candidate(environment,candidate,base)
        candidates[environment] = candidate
    source=ROOT/'source.env'
    content=source.read_text().replace('DESTINATION=g5','DESTINATION=google-drive')
    with tempfile.NamedTemporaryFile(dir=ROOT, delete=False) as f:
        staged=Path(f.name);f.write(content.encode());f.flush();os.fsync(f.fileno())
    try:
        os.replace(staged,source)
        fd=os.open(ROOT,os.O_DIRECTORY|os.O_RDONLY)
        try:os.fsync(fd)
        finally:os.close(fd)
    finally:staged.unlink(missing_ok=True)
    for environment,candidate in candidates.items():
        atomic_json(state/f'{environment}.last-success.json',candidate)
    atomic_json(state/'drive-activated.json',{'folder_id':FOLDER,'quota_free_bytes':quota['free'],'synthetic_retry_collision_and_deletion':'passed','production_and_staging':'verified'})
    (state/'drive-activation-failed.json').unlink(missing_ok=True)
    print('Sideyard Drive backups verified and activated')


if __name__=='__main__':
    try:
        private_directory(STATE)
        with (STATE/'drive-activation.lock').open('a') as lock:
            fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
            activate()
    except (OSError,ValueError,KeyError,subprocess.SubprocessError):
        atomic_json(STATE/'drive-activation-failed.json', {'failed_at': int(time.time())})
        try:
            from monitor import notice
            notice(settings(ROOT/'notify.env'), 'production', 'drive-activation-'+str(int(time.time())//86400),
                   'Sideyard Google Drive backup activation failed. Inspect sideyard-backup-drive.service on G5.')
        except (OSError, ValueError, KeyError):
            pass  # The durable failure record and failed unit remain inspectable.
        raise SystemExit('Drive activation incomplete; inspect protected credentials and backup service logs')
