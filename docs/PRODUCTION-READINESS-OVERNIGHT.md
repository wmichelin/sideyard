# ClubDue overnight production-readiness run

This is the durable execution checklist for the ClubDue roadmap loop. The loop
owns one coherent change at a time, records evidence in its GitHub master issue,
and verifies each checkpoint on staging.

Staging: https://clubdue-staging.michelindigitalmedia.com

Production is out of scope. Never run `deploy-production.yml` without Walter's
explicit approval in chat.

## Operating contract

- Check open `agent-ready` issues before roadmap work. Customer P0/P1 work
  preempts the next roadmap slice.
- One execution agent owns repository writes, commits, pushes, and staging
  verification. Specialist agents review and advise only.
- Before a non-trivial slice, record the product, architecture, unit-testing,
  staff-engineering, DevOps, and QA recommendations in the master issue.
- Keep changes small, additive, and reversible. Do not overwrite unrelated
  work or use destructive git operations.
- Use an isolated disposable Postgres for tests. If host Go is unavailable,
  use the official Go 1.23 container.
- A transient T3, GitHub, CI, deployment, network, browser, or notification
  failure is a recovery task, not a reason to abandon the run.
- Do not start the next slice until local `main`, `origin/main`, CI, the staging
  deployment, and the staging smoke result are reconciled for the same commit.
- Do not add real secrets, send real email, charge cards, or touch Pantry
  (`:8080`) or Headscale (`:8081`).

## Required slices

### P0.1 — Run Postgres integration tests in CI

- [x] Add a disposable Postgres 16 service to CI.
- [x] Run the tenant, snapshot, renewal, digest, and visit-entitlement tests
      with `DATABASE_URL` configured.
- [x] Make CI fail if the required integration tests skip.
- [x] Preserve a fast local unit-test path and static binary build.

Acceptance: CI proves the database suite executed and `go test -count=1 ./...`
plus `CGO_ENABLED=0 go build ./...` pass.

### P0.2 — Validate configuration and fail closed

- [x] Introduce one typed configuration source for environment, database,
      session secret, public URL, development routes, and runtime limits.
- [x] Permit implicit demo mode only in development.
- [x] Require Postgres, a strong session secret, and an HTTPS public URL in
      staging/production.
- [x] Reject development routes and the test-payment stub in production.
- [x] Make production pilot enrollment invite-only or allowlist-controlled;
      reject unapproved public signup.
- [x] Add a documented, auditable owner recovery/provisioning path that does
      not require ad hoc database edits.
- [x] Cover safe and unsafe environment combinations with table-driven tests.

Acceptance: unsafe production configuration exits nonzero with a secret-free
error; staging retains its explicit seeded workflow; an approved pilot owner
can be provisioned and recover access while an unapproved signup is rejected.

### P0.3 — Harden HTTP and authentication boundaries

- [x] Set secure session-cookie attributes outside development.
- [x] Add CSRF protection to every state-changing form.
- [x] Add request and multipart upload limits.
- [x] Add baseline security headers and bounded login/signup throttling.
- [x] Rotate sessions after authentication and retain generic login failures.
- [x] Verify every registered mutation route rejects missing/invalid CSRF and
      security logs contain no customer email, token, or homeowner data.
- [x] Add handler tests for success, rejection, limit, and throttling paths.

Acceptance: missing/invalid CSRF is rejected, oversized import is rejected,
auth throttling recovers after its window, and normal staging forms still work.

### P0.4 — Add runtime readiness and observability

- [x] Configure HTTP server timeouts and graceful signal shutdown.
- [x] Add process-only `/livez` and database-aware `/readyz`.
- [x] Add request IDs and completion logs without secrets or homeowner data.
- [x] Surface background renewal/digest errors with useful context.
- [x] Make deployment readiness depend on `/readyz`.

Acceptance: readiness fails when Postgres is unavailable, liveness remains
process-only, and shutdown closes HTTP/database resources cleanly.

### P1.1 — Prepare production deployment parity

- [x] Add an isolated ClubDue production Postgres/network/volume/env contract,
      without provisioning or deploying it during this run.
- [x] Ensure staging and production cannot share data, secrets, or networks.
- [x] Add backup, retention, and disposable restore-rehearsal commands.
- [x] Document and test backup encryption, access, deletion, and retention.
- [x] Add a production-mode rehearsal using disposable infrastructure.
- [x] Keep production smoke credentials in GitHub secrets, never the repo.

Acceptance: a disposable production-mode rehearsal reaches readiness, login,
and an authenticated dashboard; backup output restores successfully; deployment
scripts cannot target ports 8080 or 8081.

### P1.2 — Make renewal handling pilot-safe

- [x] Never render or accept the fake payment action in production mode.
- [x] Show an honest contact-the-shop state when real payment is unavailable.
- [x] Make future, due-today, and past-due imported active agreements produce
      explicit deterministic renewal work; never roll an overdue cycle out of
      the queue silently.
- [x] Add customer contact details, property, agreement link, and due state to
      the renewal workbench.
- [x] Render renewal mutation errors instead of silently redirecting.
- [x] Replace click-only rows with semantic, accessible links.
- [x] Label visit handoff truthfully: “scheduled” currently records a Jobber
      handoff/click time, not an appointment time.
- [x] Reconcile README and demo claims with actual renewal behavior.

Acceptance: production-mode tests prove the fake CTA is absent, renewal errors
are visible, representative future/due/past-due imports remain actionable, and
the enriched workflow passes desktop/mobile staging checks.

### P1.3 — Expand smoke coverage and runbooks

- [ ] Exercise liveness, readiness, CSRF-aware login, dashboard, agreements,
      a renewal mutation, a visit-status mutation, and visit export in staging
      smoke tests.
- [ ] Verify production mode has no development routes or fake payment action.
- [ ] Document configuration, provisioning, backup/restore, rollback, staging
      verification, production approval, and pilot limitations.
- [ ] Run the final Postgres-backed suite, build, diff check, exact-SHA CI and
      staging deployment checks, authenticated smoke, and focused acceptance.

Acceptance: every required box is checked in a committed revision; CI and the
staging deployment succeeded for that exact revision; staging smoke and focused
checks passed afterward.

### P1.4 — Define and instrument the controlled pilot

- [ ] Document an invite-only, one-trusted-owner pilot: duration, eligibility,
      assisted onboarding, manual payment, CSV-only Jobber handoff, no real
      email, supported plan shapes, support/escalation, and exit/rollback.
- [ ] Add pilot-facing privacy, data-handling/retention/deletion, limitations,
      support, and consent guidance; clearly flag any terms requiring human or
      legal approval rather than presenting them as approved.
- [ ] Add a concierge import checklist, representative fictional sample CSV,
      first-run checklist, export/correction procedure, and feedback cadence.
- [ ] Reconcile the demo script and README with implemented behavior; add a
      concise pilot overview and honest capabilities/limitations FAQ.
- [ ] Instrument the activation funnel without homeowner PII: invited,
      account created, import previewed/committed, first renewal/visit action,
      and returning use.
- [ ] Rehearse a fresh-shop onboarding and record import reconciliation,
      time-to-first-value, workflow failures, and manual workarounds.

Acceptance: a representative fictional pilot reaches first value in under 15
assisted minutes; the operator can export/correct data; pilot limitations and
manual requirements are explicit; instrumentation emits no homeowner PII.

## Stretch slice

Only after all required slices are green:

- [ ] Add a provider-neutral mail interface and fake-SMTP tests for claiming,
      retrying, and recording digest deliveries.
- [ ] Leave delivery disabled safely when SMTP is unconfigured.
- [ ] Do not send real email during this run.

## Completion protocol

The execution agent may emit `CLUBDUE_PRODUCTION_READINESS_COMPLETE` only when:

1. every required checkbox above is checked in `origin/main`;
2. all tests and the static build pass;
3. CI and Deploy Staging succeeded for the exact `origin/main` SHA;
4. authenticated staging smoke and focused checks passed after deployment;
5. the master issue contains the commits, commands, workflow links, staging
   result, remaining manual requirements, and confirmation that production was
   untouched.

If all implementable work is complete but only credentials, vendor choices, or
explicit production approval remain, emit
`CLUBDUE_PRODUCTION_READINESS_MANUAL_ONLY` with those items listed. Any other
failure must stay in recovery and resume the same slice or another independent
required slice.
