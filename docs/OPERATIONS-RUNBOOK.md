# Pilot operations runbook

This runbook supports the controlled ClubDue pilot. It does not authorize a
production deployment, create credentials, send email, or process payments.

## Configuration and provisioning

- Use the typed settings described in the [README](../README.md#configuration).
  Staging and production fail closed without a Postgres `DATABASE_URL`, a
  strong `SESSION_SECRET`, and an HTTPS `PUBLIC_URL`.
- Staging is the only environment provisioned by normal pushes. It uses the
  isolated `clubdue-staging-db`, `clubdue-staging-net`, and persistent
  `clubdue-staging-pgdata` resources described in [DEPLOY.md](DEPLOY.md).
- Production resources are a prepared, separate contract only. Do not create,
  reuse, or copy staging database volumes, networks, environment files, or
  secrets into production. Follow the root-only environment-file and owner
  provisioning procedures in [DEPLOY.md](DEPLOY.md) and
  [OWNER-PROVISIONING.md](OWNER-PROVISIONING.md) only after approval.

## Staging verification

After every push to `main`, wait for its exact Deploy Staging workflow and run:

```bash
./scripts/smoke-test.sh staging
```

The staging smoke seeds only the isolated demo shop, performs CSRF-aware login,
checks liveness/readiness, dashboard and agreements, saves a non-final renewal
outcome, records a Jobber handoff, and verifies the visit CSV export. It uses
the staging demo account locally and must never be aimed at production.

## Backup, restore, and rollback

- Use the authenticated encrypted backup and isolated restore-rehearsal
  commands in [DEPLOY.md](DEPLOY.md#encrypted-postgres-backups-and-restore-rehearsal).
  Backups and restores need distinct approved secret-manager values for
  `CLUBDUE_BACKUP_PASSPHRASE` and `CLUBDUE_BACKUP_HMAC_KEY`, plus a disposable
  restore target. Keep each `*.sql.enc` file paired with its required
  `*.sql.enc.mac` sidecar; restore intentionally fails before decryption or
  database access if either is missing, malformed, or does not authenticate.
  Never restore into staging or overwrite a running production database.
- Rotate the encryption and HMAC secrets only after an isolated successful
  restore rehearsal. Keep prior secret versions until their retained backup
  pairs are no longer recoverable by policy, and record only key identifiers,
  dates, and rehearsal evidence in the recovery record—never secret material.
- To correct a staging release, make a small fix-forward commit or revert the
  bad commit on `main`, push it, wait for the replacement exact-SHA CI and
  staging deployment, then rerun the staging smoke. Preserve the failing
  workflow evidence in the release record.
- Production rollback is a human-operated, approved change. Do not use this
  runbook to run a production workflow or alter the production deployment
  variable. Stop, preserve evidence, and obtain explicit chat approval plus
  the GitHub production-environment approval before any production action.

### Application rollback recovery path

This recovery path changes only the ClubDue nginx upstream; it does not touch
Pantry, Headscale, Postgres data, backups, or credentials. A normal rollout
leaves one `clubdue-previous` container running on the other ClubDue loopback
port (`8083`/`28083`). After the required production approvals:

1. Record the incident time, current image ID, retained previous image ID, and
   both local ports.
2. Dispatch the reviewed **Deploy Production** GitHub workflow with
   `mode=rollback`; do not SSH to the droplet or invoke the helper directly.
3. The workflow invokes the helper with `CONTAINER=clubdue`, `PORT=8083`, and
   `DOMAIN=clubdue.michelindigitalmedia.com`. The helper verifies
   `clubdue-previous` readiness, validates the uniquely
   identified ClubDue vhost, points only that vhost at the retained release,
   reloads nginx, and swaps the current/previous container names.
4. Run the read-only production smoke with the approved smoke account and
   record its result. If the retained container is not ready, the vhost is not
   uniquely identifiable, or nginx rejects the edit, stop—the active release
   is preserved and manual investigation is required.

Do not use application rollback after a migration that is not backwards
compatible with the retained binary. Restore testing is an isolated database
recovery procedure, not an application rollback shortcut.

## Production approval and pilot limits

Production requires both explicit approval in chat and the GitHub production
environment review described in [RELEASE-WORKFLOW.md](RELEASE-WORKFLOW.md).
Staging verification is necessary but is never approval to promote.

The current pilot is intentionally limited: one trusted owner, assisted
onboarding, manual payment contact, CSV-only Jobber handoff, no real email, and
no automatic appointment scheduling. A "handed off" visit is not an appointment
time, and the production homeowner renewal page has no payment action. Treat
customer data, credential recovery, exports, backups, and deletion requests as
operator-owned procedures. Policy or legal terms require human approval; this
document does not present them as approved.
