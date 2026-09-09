# Sideyard delivery status

Status: implementing — business setup verified on staging; property records ready to stage.
Last completed: internal site details, measurement history and navigation links.
Now: publish the verified property-record slice to staging.
Next: verify property records; continue vehicle/equipment records and authored notes/photos.
Staging: https://clubdue-staging.michelindigitalmedia.com (verified for business setup; property records pending).
Blocker: none for staging. Production requires review and approval.

Implementation was started after the user's instruction to proceed on September
8, 2026. The earlier planning-only status in the execution/requirements documents
describes their original handoff; this file tracks subsequent implementation.

## Completed locally

- Six trade presets plus General services; primary and additional trade selection.
- Explicit preview and service selection before applying defaults.
- Editable/disableable starter service names and descriptions; opt-in only.
- Stable preset service identities and stored version 1. Reapplication preserves
  custom edits and disabled services. Concurrent/stale edits are rejected.
- Owner-only settings, tenant-scoped persistence, CSRF protection and bounded
  public-demo mutations. Reads and previews create no business records.
- Additive migration 021; existing plans, agreements and schedules are untouched.

Starter services are preparation records, not priced plan templates or scheduled
jobs. Field selections, pricing, checklists and future preset-version upgrades
will be connected as those capabilities ship. This is the business setup
foundation of Release 1, not completion of the entire execution plan.

## Verification evidence

- Go 1.23 container: `go vet ./...`, `go test -count=1 ./...` with required real
  Postgres integration, and static binary build passed.
- New tests cover mixed/invalid trades, read-only preview, tenant isolation,
  edited/disabled service preservation, duplicate suppression, concurrent saves,
  stale edits and absence of unintended agreements/plans/schedules.
- Existing CSRF and bounded demo-route tests include `/business`.
- Production backup/restore/demo rehearsal and pilot onboarding rehearsal passed.
- Playwright at 390 and 1440 pixels passed preview, apply, edit, reapply, no
  horizontal document overflow and no browser errors. Mobile screenshot reviewed.
- Initial browser login failed because the disposable database already contained
  integration-test tenants and bootstrap correctly skipped it. Provisioned a
  synthetic local owner with the existing operator command; browser rerun passed.

## Property-record slice

- Owner-only property page linked from each customer's service locations.
- Editable internal site/access details with concurrent-edit protection.
- Append-only measurement history: named zones, twelve area/length types, exact
  decimal values, dimension-appropriate units, source, measured date, estimated
  flag and recording author. Latest 100 entries displayed; all entries retained.
- Separate turf, bed, driveway, walkway, patio, roof, building-footprint, floor,
  exterior-wall and perimeter measurements. Unknown measurements remain absent.
- Google/Apple Maps links contain only the service address, not internal details.
- Migration 022 adds tenant-safe property references and measurement unit/value
  constraints. New data does not enter legacy visit exports or public pages.
- Focused property tests, full fresh-Postgres suite, vet, static build, production
  rehearsal and pilot rehearsal passed. Browser checks passed at 390/1440 pixels
  with saved notes, preserved decimal history, escaped text and navigation links.
- The first full-suite retry against the reused database collided with the
  pre-existing fixed renewal-token fixture in `clock_test.go`. Rerunning on a new
  disposable database passed; no unrelated test behavior was changed.

## Staging evidence

- Business setup source: `a6beead76c1a28b4d85bd80a8931c99a278d0548`.
- CI `34296690579` and staging `34296690593` succeeded.
- Manual staging smoke and mixed-trade mobile/desktop browser walkthrough passed.
- The first staging browser script used an ambiguous status selector because the
  demo banner also has role=status. Narrowing to the save message fixed the QA
  script; the app required no change.
- Production remains on the prior approved release.

## Remaining work

Use [execution plan](SIDEYARD-EXECUTION-PLAN.md) for the seven releases and billing
patterns. Site details and manual measurement history are implemented; vehicle
assets, expanded equipment editing, authored visit notes and private photos remain.
Billing, scheduling extensions and provider integrations remain as planned. No
paid integrations or live customer payment collection are configured by this change.
