# Property address correction delivery

Status: testing
Last completed: full checks and local phone/desktop walkthrough passed.
Now: pushing verified address correction to staging.
Next: staging smoke and browser verification.
Staging: https://clubdue-staging.michelindigitalmedia.com (pending for this slice)
Blocker: none; production requires separate approval.

## Delivery brief and team review

Continue E2.1: owners can correct an existing property's address, including its
unit/suite, without deleting or recreating the site. A different service location
should be a new property, keeping the old location's records together.

- Product manager: accept corrections to address, unit, city, state and postal
  code. Show errors with entered text retained. Keep notes, measurements,
  equipment, agreements and work attached to the existing property ID.
- Architect: add an address revision to properties. A database trigger advances
  it for address changes from all writers, including existing CSV imports, so an
  open browser cannot overwrite an import unnoticed. Owner-only editing; all
  reads and writes are tenant scoped. Existing display/routes read current data.
- Unit-testing expert: cover another tenant, blank/oversized input, optional unit,
  concurrent/stale saves, import-style direct updates and retained child records.
- Staff software engineer: use a focused GET/POST address route and HTML form.
  Keep optimistic revision on failed forms; require reload after conflict. Do not
  change customer ownership or add property deletion/move behavior.
- DevOps engineer: additive migration and no dependency changes; trigger remains
  compatible with previous binaries. Existing backup/restore captures addresses.
  Run the required suites/rehearsals and deploy only to staging.
- QA tester: correct a synthetic address and unit, verify updated map destination
  and customer summary, preserve measurements/notes/equipment, and exercise a
  stale tab at phone/desktop sizes.

Decision: proceed. Address corrections update existing current-address displays,
including legacy work/export views; historical address snapshots for new quotes
and contracts belong to E4. This editor is for correcting a site, not relocating
its service history to a different site.

## Local verification

- Focused address, CSRF and demo request tests passed with Postgres 16.
- Go 1.23 container: `go vet ./...`, `go test -count=1 ./...` with required
  Postgres integration, and a static `./cmd/clubdue` build passed.
- Production backup/restore and pilot onboarding rehearsals passed.
- `node /tmp/sideyard-qa/address.cjs` passed at 390/1440 pixels: retained site
  notes/measurements/equipment, corrected customer summary and map destinations,
  optional suite and stale-tab conflict. Phone screenshot inspected.
- Initial browser script used Playwright's single-page convenience context;
  changed the QA script to an explicit context for its stale-tab check. The app
  required no change and the rerun passed.
