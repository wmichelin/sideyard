#!/usr/bin/env python3
"""Accept bounded HMAC-authenticated off-host delivery receipts over fixed SSH."""
import hashlib
import hmac
import json
import os
from pathlib import Path
import re
import time
from archive import ARCHIVE_ID, atomic_json, canonical, private_directory, settings

DOMAIN=b'Sideyard off-host delivery receipt v1\n'
ROOT=Path('/var/lib/sideyard-backup-receipts')


def open_receipt(data,key,environment,now):
    if not key or len(data)>8192:
        raise ValueError('Invalid receipt')
    item=json.loads(data);payload=item['payload']
    expected=hmac.new(key,DOMAIN+canonical(payload),hashlib.sha256).hexdigest()
    if not hmac.compare_digest(item['hmac'],expected):raise ValueError('Receipt authentication failed')
    if payload['environment']!=environment or payload['destination'] not in ('g5','google-drive') or not ARCHIVE_ID.fullmatch(payload['archive']) or not re.fullmatch('[0-9a-f]{40}',payload['source_revision']) or not isinstance(payload['verified_at'],int) or isinstance(payload['verified_at'],bool) or payload['verified_at']>now+300 or not payload.get('data_generation'):
        raise ValueError('Receipt scope or timestamp invalid')
    return payload


def record(environment,incoming):
    if environment not in ('production','staging'):raise ValueError('Unsupported receipt environment')
    config=settings('/etc/sideyard-backup/'+environment+'-recovery.env')
    data=incoming.read(8193)
    payload=open_receipt(data,config['CLUBDUE_BACKUP_HMAC_KEY'].encode(),environment,int(time.time()))
    if abs(time.time()-payload['verified_at'])>300:raise ValueError('Receipt is not current')
    root=private_directory(ROOT)
    target=root/(environment+'.json')
    if target.exists():
        old=open_receipt(target.read_bytes(),config['CLUBDUE_BACKUP_HMAC_KEY'].encode(),environment,int(time.time()))
        if old['verified_at']>payload['verified_at']:raise ValueError('Receipt would move freshness backward')
    atomic_json(target,json.loads(data))
