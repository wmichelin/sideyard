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

The staging smoke checks liveness/readiness, verifies signup and development
routes are closed, and proves the former shared demo login is retired. It then
creates two independent ephemeral landscaping tenants, completes recurring
work, saves a renewal outcome, exercises an ordinary setup form, proves that
write is isolated from the second tenant, exercises the bounded `/import` CSV
preview and commit, rejects multipart on every other path, and checks the
service-work CSV export. It uses fictional staging fixtures only. A failed workflow smoke automatically switches traffic
back to the retained previous staging container and leaves the workflow failed
for investigation. Staging rollback also requires the retained release's
`demo-sandbox-v1` safety marker. During the first transition from the former
shared demo, an unmarked rollback is deliberately refused so the old shared,
unrestricted tenant cannot be reopened; use a reviewed fix-forward instead.

Public-demo operations:

- `ENABLE_PUBLIC_DEMO=1` is valid with `APP_ENV=staging` or `production` only.
  Staging forces it on for release review. Production requires the explicit
  `enable_public_demo=true` promotion input; the default is off. Disable the
  input and redeploy for the kill switch.
- Workspaces and sessions expire together after four hours. At expiry they stop
  resolving; scheduled maintenance and new demo creation delete expired tenants
  and their child rows through a narrowly scoped cascade. Encrypted production
  backups may retain submitted data for up to 30 days.
- The active-workspace cap is 100. A capacity response creates no session and
  should prompt review of expiry cleanup and database health, not a cap bypass.
- Ordinary URL-encoded product forms are writable, but each sandbox admits only
  30 state-changing requests over its lifetime. An admitted request consumes
  one slot even when handler validation later rejects it. A `429` stating that
  the 30 actions were used means the visitor must start a fresh demo; do not
  bypass or reset the counter. An `/import` preview and its commit each consume
  one of these slots even though preview creates no product records.
- Demo form bodies are limited to 16 KiB and each field value to 2,000 bytes.
  Oversized forms receive `413`. The sole multipart exception is the exact
  `/import` preview: it accepts one `.csv`, at most 100 data rows and 128 KiB
  raw, with no cell over 2,000 bytes. A sandbox may admit at most 500 uploaded
  data rows over its lifetime. Across committed imports, it may also admit at
  most 20,000 projected recurring visits from active recurring rows for the
  same 30-days-back through 90-days-ahead window used to materialize visits.
  Preview creates no customer,
  property, agreement, or service records; it holds the exact CSV server-side
  for at most 15 minutes. Commit requires the expiring, tenant-bound,
  single-use token returned for that exact preview. Every other
  multipart request remains blocked with `403`; do not enable other uploads or
  connectors for a public demo.
- A separate 20-actions-per-minute limiter may also return `429` with
  `Retry-After: 60`; wait for the window instead of raising the limit.
- Use fictional or fully redacted input only and never put real customer
  information into a demo workspace. Logs record only request IDs and aggregate
  creation/failure text.
- `ENABLE_DEV_ROUTES` is development-only. Do not restore the retired shared
  staging account; provision allowlisted pilot owners through the audited path.
- Production rehearsal proves the explicit opt-in, isolated write path, owner
  separation, and disabled development routes. Production smoke uses separate
  owner and demo cookie jars, makes only bounded fictional demo writes, proves
  a second visitor cannot see them, and rechecks that the real owner tenant did
  not receive them. A disabled release must return 404 from `/demo` and omit its
  login CTA.

For production verification after an approved promotion, set the smoke
expectation explicitly:

```bash
CLUBDUE_SMOKE_PUBLIC_DEMO=enabled \
  CLUBDUE_SMOKE_EMAIL='<approved owner>' \
  CLUBDUE_SMOKE_PASSWORD='<approved secret>' \
  ./scripts/smoke-test.sh production
```

Never print or record the owner credentials, cookies, or returned HTML. If the
production smoke fails, preserve evidence and use the approved workflow's
explicit rollback or a reviewed kill-switch release; production does not
automatically roll back after an external smoke failure.

## Backup, restore, and rollback

The Azure rollout adds [CLI-managed infrastructure and seven-day encrypted
exports](../infra/azure/README.md). Check [delivery status](SIDEYARD-DELIVERY-STATUS.md)
for which environment is actually connected. Historical backup files keep their
previous retention. New backups use the versioned 39-byte authentication sidecar;
old public-checksum sidecars require explicit isolated legacy recovery.

The native database operator is currently staging-only. Use the
[target-checking Azure wrapper](../infra/azure/README.md#staging-sql-operator-boundary),
which rejects production, client-secret auth, an implicit `.env`, a PostgreSQL
URL, and a server/database that does not match staging. Run the read-only check
before and after any migration or maintenance window. Azure SQL web startup is
still disabled, so these commands do not switch staging traffic away from its
current PostgreSQL database.

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
leaves one stopped `clubdue-previous` container on the other ClubDue loopback
port (`8083`/`28083`) with restart disabled. After the required production approvals:

1. Record the incident time, current image ID, retained previous image ID, and
   both local ports.
2. Dispatch the reviewed **Deploy Production** GitHub workflow with
   `mode=rollback`; do not SSH to the droplet or invoke the helper directly.
3. The workflow invokes the helper with `CONTAINER=clubdue`, `PORT=8083`, and
   `DOMAIN=getsideyard.com`. The helper verifies
   backend and data generation against the root-owned host fence, starts
   `clubdue-previous` with restart disabled, verifies readiness, validates the uniquely
   identified ClubDue vhost, points only that vhost at the retained release,
   reloads nginx, and swaps the current/previous container names.
4. Run the read-only production smoke with the approved smoke account and
   record its result. If the retained container is not ready, the vhost is not
   uniquely identifiable, or nginx rejects the edit, stop—the active release
   is preserved and manual investigation is required.

Do not use application rollback after a migration that is not backwards
compatible with the retained binary. Restore testing is an isolated database
recovery procedure, not an application rollback shortcut.
The helper always rejects cross-backend or cross-generation rollback before
starting the retained container. After Azure receives writes, use fix-forward
or a separately reviewed freeze, reverse transfer and reconciliation.

## Production approval and pilot limits

Production requires both explicit approval in chat and the GitHub production
environment review described in [RELEASE-WORKFLOW.md](RELEASE-WORKFLOW.md).
Staging verification is necessary but is never approval to promote.

The current real-data pilot is intentionally limited: trusted owners, assisted
onboarding, manual payment contact, CSV-only generic handoff, no real email,
and no automatic appointment scheduling. A "handed off" service is not an
appointment time, and the production customer renewal page has no payment action. Treat
customer data, credential recovery, exports, backups, and deletion requests as
operator-owned procedures. Policy or legal terms require human approval; this
document does not present them as approved.
