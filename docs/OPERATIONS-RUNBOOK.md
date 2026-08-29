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

- Use the encrypted backup and isolated restore-rehearsal commands in
  [DEPLOY.md](DEPLOY.md#encrypted-postgres-backups-and-restore-rehearsal).
  Backups and restores need an approved secret-manager passphrase and a
  disposable restore target; never restore into staging or overwrite a running
  production database.
- To correct a staging release, make a small fix-forward commit or revert the
  bad commit on `main`, push it, wait for the replacement exact-SHA CI and
  staging deployment, then rerun the staging smoke. Preserve the failing
  workflow evidence in the release record.
- Production rollback is a human-operated, approved change. Do not use this
  runbook to run a production workflow or alter the production deployment
  variable. Stop, preserve evidence, and obtain explicit chat approval plus
  the GitHub production-environment approval before any production action.

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
