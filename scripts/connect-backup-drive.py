#!/usr/bin/env python3
"""Connect only Sideyard's Drive destination; keep Pantry configuration unchanged."""

import argparse
import configparser
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

SOURCE = Path('/etc/pantry-backup/rclone.conf')
DESTINATION = Path('/etc/sideyard-backup/rclone.conf')
RCLONE = '/usr/local/bin/rclone'
PARENT_ID = '1YGNmmyH0d9tzvNr9B01I6JD9hpk1UN9z'
FOLDER_ID = '1CZlgUMeimavN-IyLKZ2Do9T2IaHjFkxL'
FOLDER_NAME = 'ClubDue Production Backups - G5'


class SetupError(Exception):
    """A credential-free message safe to display to the operator."""


def configuration(raw, remote, folder_id):
    parsed = configparser.ConfigParser(interpolation=None)
    parsed.read_string(raw.decode())
    if remote not in parsed or parsed[remote].get('type') != 'drive':
        raise SetupError('Expected the existing Google Drive remote.')
    if parsed[remote].get('root_folder_id') != folder_id:
        raise SetupError('Existing remote points at an unexpected folder; left unchanged.')
    return dict(parsed[remote])


def clubdue_configuration(values):
    parsed = configparser.ConfigParser(interpolation=None)
    parsed['sideyard-drive'] = values
    parsed['sideyard-drive']['root_folder_id'] = FOLDER_ID
    output = io.StringIO()
    parsed.write(output)
    return output.getvalue().encode()


def verify_folder(config):
    # Lookup the child through the known parent and require its exact ID.
    result = subprocess.run(
        [RCLONE, '--config', str(config), '--drive-root-folder-id', PARENT_ID,
         'lsjson', '--stat', '--dirs-only', f'sideyard-drive:{FOLDER_NAME}'],
        capture_output=True, text=True, timeout=60,
    )
    if result.returncode:
        # OAuth diagnostics can contain sensitive details. Do not print them.
        raise SetupError('Drive access failed. Run ./connect-backup-drive.sh to sign in again.')
    item = json.loads(result.stdout)
    if isinstance(item, list):
        item = item[0] if len(item) == 1 else {}
    if item.get('ID') != FOLDER_ID or not item.get('IsDir'):
        raise SetupError('Drive returned an unexpected folder; configuration was not installed.')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true', help='Check access without signing in or saving changes.')
    args = parser.parse_args()
    if os.geteuid() != 0:
        raise SetupError('Use ./connect-backup-drive.sh so credentials remain protected by root.')
    os.umask(0o077)
    source_bytes = SOURCE.read_bytes()
    values = configuration(source_bytes, 'pantry-drive', PARENT_ID)
    if DESTINATION.parent.is_symlink() or DESTINATION.is_symlink():
        raise SetupError('Refusing a symlink at the Sideyard configuration path.')
    if DESTINATION.exists():
        values = configuration(DESTINATION.read_bytes(), 'sideyard-drive', FOLDER_ID)

    with tempfile.TemporaryDirectory(prefix='sideyard-drive-connect-', dir='/run') as work:
        config = Path(work) / 'rclone.conf'
        config.write_bytes(clubdue_configuration(values))
        if not args.check:
            print('Sign in with the Google account used for Pantry backups.', flush=True)
            print('This updates only Sideyard’s copied login; Pantry’s configuration stays unchanged.', flush=True)
            print('Follow rclone’s browser instructions. Keep any authorization tokens in this terminal.', flush=True)
            # Interactive OAuth runs only in the user's terminal, never --check.
            result = subprocess.run([RCLONE, '--config', str(config), 'config', 'reconnect', 'sideyard-drive:'])
            if result.returncode:
                raise SetupError('Sign-in did not complete; existing configuration was preserved.')
        verify_folder(config)
        configuration(config.read_bytes(), 'sideyard-drive', FOLDER_ID)
        if SOURCE.read_bytes() != source_bytes:
            raise SetupError('Pantry configuration changed during this run; retry before installing.')
        if not args.check:
            DESTINATION.parent.mkdir(mode=0o700, exist_ok=True)
            if DESTINATION.parent.stat().st_uid != 0 or DESTINATION.parent.stat().st_mode & 0o077:
                raise SetupError('Sideyard configuration directory must be root-owned and mode 0700.')
            staged = None
            try:
                with tempfile.NamedTemporaryFile(dir=DESTINATION.parent, prefix='.rclone-', delete=False) as f:
                    staged = Path(f.name)
                    f.write(config.read_bytes())
                    f.flush()
                    os.fsync(f.fileno())
                os.replace(staged, DESTINATION)
            finally:
                if staged is not None:
                    staged.unlink(missing_ok=True)
    print('Sideyard Drive folder verified.' if args.check else 'Sideyard Drive connected and saved securely. The installed backup watcher will verify uploads and activate Drive automatically.')


if __name__ == '__main__':
    try:
        main()
    except SetupError as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
    except (ValueError, OSError, configparser.Error, subprocess.TimeoutExpired):
        # Deliberately avoid exception text: parsers and subprocesses may include secrets.
        print('Connection incomplete. Check the selected Google account, authentication, and protected configuration; no backup was started.', file=sys.stderr)
        sys.exit(1)
