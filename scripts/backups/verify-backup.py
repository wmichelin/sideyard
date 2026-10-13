#!/usr/bin/env python3
"""Verify a Sideyard set; optional restore is limited to disposable containers."""
import argparse
import json
import os
from pathlib import Path
import subprocess
from archive import settings,verify


def check(environment,folder,container=None):
    config=settings('/etc/sideyard-backup/'+environment+'-recovery.env')
    payload=verify(Path(folder),environment,config['CLUBDUE_BACKUP_HMAC_KEY'].encode())
    if container:
        if not container.startswith('sideyard-recovery-'):
            raise ValueError('Restore requires a disposable sideyard-recovery-* container')
        item=json.loads(subprocess.run(['docker','inspect',container],check=True,capture_output=True).stdout)[0]
        host=item['HostConfig']
        if host['NetworkMode']!='none' or host.get('Privileged') or host.get('Binds') or host.get('Devices') or host.get('CapAdd') or host.get('PidMode')=='host' or host.get('IpcMode')=='host' or any(m['Type']!='tmpfs' for m in item['Mounts']):
            raise ValueError('Restore target must have no network, privileges or durable mounts')
        query="SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_type='BASE TABLE'"
        count=subprocess.run(['docker','exec',container,'psql','-U','clubdue','-d','clubdue','-At','-c',query],check=True,capture_output=True).stdout.strip()
        if count!=b'0':
            raise ValueError('Restore requires an empty target database')
        subprocess.run(['bash',str(Path(__file__).with_name('postgres-restore.sh')),str(Path(folder)/'database.sql.enc')],check=True,env={**os.environ,**config,'CLUBDUE_RESTORE_DB_CONTAINER':container})
    return {'environment':environment,'archive':payload['id'],'source_revision':payload['source_revision'],'authenticated':True,'restored':bool(container)}


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('environment',choices=['production','staging'])
    parser.add_argument('folder')
    parser.add_argument('--restore-container')
    args=parser.parse_args()
    try:print(json.dumps(check(args.environment,args.folder,args.restore_container)))
    except (OSError,ValueError,KeyError,subprocess.SubprocessError):raise SystemExit('Backup verification/isolated restore failed')
