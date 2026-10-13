#!/usr/bin/env python3
"""Droplet-side check catches G5 outage without depending on G5 connectivity."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import time
from archive import atomic_json,private_directory,settings
from monitor import notice

spec=importlib.util.spec_from_file_location('receipt',Path(__file__).with_name('record-receipt.py'))
receipt=importlib.util.module_from_spec(spec);spec.loader.exec_module(receipt)
ROOT=Path('/var/lib/sideyard-backup-receipts')


def check(environment):
    os.umask(0o077);root=private_directory(ROOT);now=int(time.time())
    config=settings('/etc/sideyard-backup/'+environment+'-recovery.env')
    state=Path('/var/lib/sideyard-operations')/(environment+'-backend.env')
    if state.exists():
        runtime=settings(state)
        generation=runtime['DATA_GENERATION'] if runtime['DATABASE_BACKEND']=='postgres' else 'unsupported-backend'
    elif environment=='production':
        result=subprocess.run(['docker','inspect','clubdue-production-db','--format','{{.Id}}'],check=True,capture_output=True)
        generation='legacy-postgres-container:'+result.stdout.decode().strip()
    else:generation='missing-runtime-state'
    marker={}
    try:marker=receipt.open_receipt((root/(environment+'.json')).read_bytes(),config['CLUBDUE_BACKUP_HMAC_KEY'].encode(),environment,now)
    except (OSError,ValueError,KeyError,TypeError):pass
    problem='' if marker.get('data_generation')==generation and now-marker.get('verified_at',0)<=15*3600 else 'No current-generation Sideyard backup delivery has been recorded in the last 15 hours. G5 or its backup runner may be unavailable.'
    incident_file=root/(environment+'.incident.json')
    try:incident=json.loads(incident_file.read_text())
    except (OSError,ValueError):incident={}
    if not isinstance(incident,dict):incident={}
    notification=settings('/etc/sideyard-backup/notify.env')
    if not problem:
        if incident.get('sent'):
            notice(notification,environment,incident['id']+'-recovered','Sideyard off-host backup delivery has recovered. A fresh signed receipt was verified on the source droplet.')
        incident_file.unlink(missing_ok=True);return
    if not incident.get('id'):
        incident={'id':'source-watch-'+str(now),'sent':False};atomic_json(incident_file,incident)
    if not incident['sent'] or now-incident.get('sent_at',0)>=86400:
        notice(notification,environment,incident['id']+'-'+str(now//86400),problem)
        incident.update(sent=True,sent_at=now);atomic_json(incident_file,incident)
        print('Sideyard missing-delivery alert accepted by email service')


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('environment',choices=['production','staging']);args=parser.parse_args()
    try:check(args.environment)
    except (OSError,ValueError,KeyError,subprocess.SubprocessError):raise SystemExit('Source backup monitor failed; inspect protected state and notification configuration')
