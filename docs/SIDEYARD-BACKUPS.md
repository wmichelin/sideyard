# Sideyard database backups

## Current operation

G5 receives fresh encrypted production and staging PostgreSQL backups through a
restricted SSH exporter on the shared droplet. The installed jobs run every
12 hours, at 07:30 and 19:30 UTC with up to five minutes of jitter. An independent
hourly monitor on G5 emails the configured Sideyard owner for failed jobs,
staleness beyond 26 hours, and recovery. The source droplet independently
checks signed off-host receipts hourly and alerts after 15 hours without a
current-generation delivery, catching G5 outages before the 24-hour objective. Notifications use the existing Resend
configuration; acceptance by Resend is verified, not inbox delivery.

Google Drive activation awaits the owner's Google sign-in. Run from the project
on G5:

```bash
./connect-backup-drive.sh
```

The helper copies Pantry's existing OAuth client configuration privately and
renews only Sideyard's login. It verifies the existing dedicated folder ID
`1CZlgUMeimavN-IyLKZ2Do9T2IaHjFkxL` before saving
`/etc/sideyard-backup/rclone.conf`. Its historical folder name is
**ClubDue Production Backups - G5**; the identity remains pinned by ID.
Keep OAuth tokens in the terminal, never in chat or source control.

The installed `sideyard-backup-drive.path` watches for that saved configuration.
Activation verifies quota, identical retry, changed-content collision refusal,
and permanent removal of its own synthetic probe. It uploads and independently
checks each environment's complete encrypted set, downloads each named set,
and restores it into a disposable, network-disabled PostgreSQL 16 container.
Only after both environments pass does it atomically switch scheduled jobs to
Drive. A failed attempt preserves G5 scheduling and records/notifies the failure.
No further deployment approval is needed for this already requested backup setup.

## Files and boundaries

- Reviewed runtime scripts: `/opt/sideyard-backup` on G5 and the source droplet.
- G5 configuration and separate recovery keys: `/etc/sideyard-backup`, root-only.
- Encrypted G5 sets: `/var/backups/sideyard-pg/{production,staging}/<archive-id>/`.
- Success, restore, incident and activation records: `/var/lib/sideyard-backup`.
- Earlier emergency copies: `/var/backups/sideyard-recovery`; their corresponding
  keys are also protected under `/etc/sideyard-backup`.
- Drive destination: `sideyard-drive:{production,staging}/<archive-id>/` within
  the pinned child folder. Never operate recursively on Pantry's parent.

The G5 key's source authorization uses OpenSSH `restrict` and one fixed exporter.
It accepts finite `backup` and authenticated `receipt` operations for production
or staging: no interactive shell,
forwarding, caller-selected paths, database names or commands. Source scripts
are root-owned and protected. The exporter acquires the existing deployment/
database-operation lock and matches the authoritative PostgreSQL generation,
application connection and fixed database container. The older production release
has no generation labels, so its signed identity binds the actual PostgreSQL
container; staging requires matching authoritative state and generation labels.
A backend switch makes mismatched/stale PostgreSQL exports fail closed.

Normal transfer creates no plaintext database archive. Ciphertext is spooled in
a private source directory with a size limit and 15-minute encryption timeout.
Each set contains `database.sql.enc`, its versioned 39-byte HMAC sidecar and a
signed manifest binding environment, source revision, data generation, timestamp,
lengths and hashes. G5 validates all three before publishing an atomic directory.
Drive components are verified first; the completion manifest is uploaded last.
A success marker is written only after the selected destination verifies.

The services run as root to read existing protected recovery credentials, with
fixed reviewed programs, owner-only state, read-only system/home restrictions,
no new privileges and narrowly writable directories. They receive no general
Docker or administrative SSH access to the droplet.

## Retention and monitoring limits

The approved seven-day recovery policy remains in effect. Expired local sets
are pruned only after a new destination success, using an exact UTC cutoff;
the newest authenticated local set survives an extended outage. Drive cleanup
permanently deletes only recognized expired directories whose complete manifests
authenticate for that environment. Unknown/incomplete sets are preserved for
operator inspection. This is not a promise that every historical or partial
artifact is erased at exactly seven days.

The two hosts monitor each other: G5 catches source-droplet/job failure; the
droplet verifies signed G5 delivery receipts and catches G5/job loss. Simultaneous
loss of both hosts still requires an external uptime monitor and independently
recoverable credentials. No new hosted monitoring vendor is configured. Recovery keys exist on the source
and G5 separately; an offline/password-manager recovery copy remains advisable.
The unrelated operator-local Azure logical-key custody ceremony was not performed
or claimed by this PostgreSQL backup work.

Existing Azure uploads fail with HTTP 403; their last recorded success was
October 9, 2026. Their old jobs and stored archives were preserved during this
transition. Retire their timers only after the replacement Drive route is
verified. This setup does not decommission Azure resources or change application
photo providers.

## Status and recovery

```bash
sudo systemctl list-timers 'sideyard-*' --all
sudo systemctl status sideyard-backup@production.service
sudo journalctl -u sideyard-backup@production.service -n 20
sudo cat /var/lib/sideyard-backup/production.last-success.json
sudo systemctl status sideyard-backup-drive.service
```

Never display the protected configuration/key files. Failure, alert acceptance
and exact archive identifiers are safe to include in release evidence.

For a named Drive recovery, download into an empty root-only directory, then
verify the complete set with the matching environment key:

```bash
sudo rclone --config /etc/sideyard-backup/rclone.conf \
  copy 'sideyard-drive:production/<archive-id>' '<empty-private-directory>' \
  --immutable --checksum
sudo python3 /opt/sideyard-backup/verify-backup.py \
  production '<private-directory>'
```

The optional `--restore-container sideyard-recovery-<name>` mode requires an
already-created empty PostgreSQL 16 database (`clubdue` user/database),
`--network none`, tmpfs-only mounts, no privileges/devices or host namespaces.
It rejects populated databases. Restore authenticates the ciphertext, finishes
decryption into anonymous memory, validates the pg_dump header and only then
accesses the target. Wrong secrets, tampering and failed decryption cannot stream
partial SQL into the target. Ensure sufficient memory for the plaintext and
disposable PostgreSQL data; remove the recovery container afterward.

New dumps exclude deployment-specific ownership/ACL statements (`--no-owner
--no-acl`) so dormant exporter roles do not prevent a fresh database restore.
Application records, schema, constraints, triggers and password hashes remain.
Reinstall required operational roles/grants using the reviewed deployment tools
before resuming their administrative workflows. Restoring into production is a
separate deliberate operator recovery decision.

See [delivery status](SIDEYARD-DELIVERY-STATUS.md) for dated live restore evidence
and [product map](PRODUCT-MAP.md) for application availability.
