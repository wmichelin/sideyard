#!/usr/bin/env python3
"""Independent G5 backup failure/freshness and recovery notifications."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import time
import urllib.request
from archive import atomic_json, private_directory, settings

ROOT = Path('/var/lib/sideyard-backup')


def problem(marker, environment, failed, now, destination):
    if not isinstance(marker, dict):
        marker = {}
    if failed:
        return 'The latest Sideyard backup job failed.'
    if marker.get('environment') != environment or marker.get('destination') != destination or not isinstance(marker.get('verified_at'), int):
        return 'No verified Sideyard backup is recorded for the configured destination.'
    age = now-marker['verified_at']
    if age < -300 or age > 26*3600:
        return 'No verified Sideyard backup has completed in the last 26 hours.'
    return ''


def notice(config, environment, incident, text, opener=None):
    if config.get('INTEREST_NOTIFY_TO') != 'wmichelin@gmail.com':
        raise ValueError('Unexpected Sideyard backup alert recipient')
    data = json.dumps({'from': config['INTEREST_NOTIFY_FROM'], 'to': ['wmichelin@gmail.com'], 'subject': f'Sideyard {environment} backups', 'text': text}).encode()
    request = urllib.request.Request('https://api.resend.com/emails', data=data, headers={'Authorization': 'Bearer '+config['RESEND_API_KEY'], 'Content-Type': 'application/json', 'User-Agent': 'Sideyard/1.0', 'Idempotency-Key': f'sideyard-g5-backups/{environment}/{incident}'})
    if opener is None:
        class NoRedirect(urllib.request.HTTPRedirectHandler):
            def redirect_request(self, *args, **kwargs):
                return None
        opener = urllib.request.build_opener(NoRedirect())
    with opener.open(request, timeout=20) as response:
        if not json.loads(response.read(4096)).get('id'):
            raise ValueError('Email service did not accept the backup notification')


def check(environment, test=False):
    os.umask(0o077)
    root = private_directory(ROOT)
    source = settings('/etc/sideyard-backup/source.env')
    config = settings('/etc/sideyard-backup/notify.env')
    incident_file = root/f'{environment}.incident.json'
    try:
        incident = json.loads(incident_file.read_text())
    except (OSError, ValueError):
        incident = {}
    if not isinstance(incident, dict):
        incident = {}
    try:
        marker = json.loads((root/f'{environment}.last-success.json').read_text())
    except (OSError, ValueError):
        marker = {}
    if source.get('DESTINATION') == 'google-drive' and isinstance(marker, dict) and marker.get('destination') != 'google-drive':
        try:
            marker = json.loads((root/f'{environment}.drive-candidate.json').read_text())
        except (OSError, ValueError):
            marker = {}
    failed = subprocess.run(['systemctl', 'is-failed', '--quiet', f'sideyard-backup@{environment}.service']).returncode == 0
    now = int(time.time())
    message = problem(marker, environment, failed, now, source.get('DESTINATION', 'g5'))
    if test:
        notice(config, environment, str(now)+'-test', 'Sideyard independent backup monitoring test. The backup service was not stopped or damaged.')
        print('Backup monitoring test accepted by email service')
        return
    if not message:
        if incident.get('sent'):
            notice(config, environment, incident['id']+'-recovered', 'Sideyard backups recovered. A new encrypted set has been verified at the configured destination.')
        incident_file.unlink(missing_ok=True)
        return
    if incident.get('problem') != message:
        incident = {'id': str(now), 'problem': message, 'sent': False}
        atomic_json(incident_file, incident)
    if not incident.get('sent') or now-incident.get('sent_at', 0) >= 86400:
        notice(config, environment, incident['id']+'-'+str(now//86400), message+' Monitoring runs on G5 independently of the source droplet.')
        incident.update(sent=True, sent_at=now)
        atomic_json(incident_file, incident)
        print('Sideyard backup alert accepted by email service')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('environment', choices=['production', 'staging'])
    parser.add_argument('--test', action='store_true')
    args = parser.parse_args()
    try:
        check(args.environment, args.test)
    except (ValueError, OSError, KeyError):
        raise SystemExit('Sideyard backup monitor failed; delivery will be retried')
