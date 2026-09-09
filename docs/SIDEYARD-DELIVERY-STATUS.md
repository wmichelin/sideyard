# Sideyard delivery status

Status: verified — four foundational slices on staging; overall execution plan remains in progress.
Last completed: authored property notes verified through the real staging browser flow.
Now: durable handoff and staging review.
Next: equipment editing and private photo storage; production promotion after approval.
Staging: https://clubdue-staging.michelindigitalmedia.com (verified through dated site notes).
Blocker: none for continued development. Production requires review and approval.

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
- Property-record source: `f8362210702c578d4db7564940eae286ad46f8d3`.
- CI `34297309111` and staging `34297309143` succeeded. Manual staging smoke
  and property-record browser checks at 390/1440 pixels passed.
- Vehicle source: `4d6a850f07eec361ea4b35242c553a78093f1f14`; CI `34297686281`
  and staging `34297686330` succeeded. Manual staging smoke and two-vehicle
  browser walkthrough passed at 390/1440 pixels.
- Latest source: `84b7340ab623375c6589770f9905b40e1bf20eb1`; CI `34298057779`
  and staging `34298057687` succeeded. Manual staging smoke and dated-note browser
  walkthrough at 390/1440 pixels passed.
- Running staging container's revision label and immutable image were read back:
  `ghcr.io/wmichelin/clubdue@sha256:003f145d3695bbbbd1ed056e2824865e08abdc527be2acfb02541343440ce9f6`.
- Production deployment gate remains false; no production promotion occurred.

## Vehicle-record slice

- A customer can have multiple vehicles without assigning them to a property.
- Editable name, make/model/year, optional identifier and internal notes, with
  separate records and concurrent-edit protection. Owner-only reading/editing.
- Migration 023 enforces tenant-safe customer ownership. Tests cover separate
  vehicles, foreign tenants/customers, stale edits, optional year, invalid input,
  property-independent identity and staff denial.
- Full fresh-Postgres suite, vet, static build, production and pilot rehearsals
  passed. Mobile/desktop browser checks created two vehicles, edited one, verified
  the other was unchanged and found no overflow or browser errors. Screenshot
  reviewed at 390 pixels.

## Dated site-note slice

- Property notes have separate event dates, recording timestamps and authors.
  Standing site/access details remain separately editable. Corrections are new
  entries, preserving earlier history. Notes are internal and owner-only.
- Migration 024 adds tenant-safe property ownership and per-shop submission
  identity. Concurrent retries acknowledge the same note; reusing a submission
  identity with different content fails instead of overwriting it.
- Validation and Postgres tests cover invalid content/dates, cross-tenant reads
  and writes, concurrent deduplication, preserved authorship/timestamps and
  correction history. Full fresh-Postgres tests, vet, static build, production
  rehearsal and pilot rehearsal passed.
- Local browser checks at 390/1440 pixels added two notes, preserved line breaks,
  escaped script-like content, displayed history and found no browser errors or
  document overflow. Screenshot inspected.

## Remaining work

Use [execution plan](SIDEYARD-EXECUTION-PLAN.md) for the seven releases and billing
patterns. Site details, manual measurement history and vehicle records are implemented;
dated property notes are also implemented. Expanded equipment editing, notes
linked directly to jobs/vehicles, staff permissions and private photos remain.
Billing, scheduling extensions and provider integrations remain as planned. No
paid integrations or live customer payment collection are configured by this change.

## Continuation details

- Migrations 021–024 are committed and applied on staging. Do not edit their
  checksummed contents; future changes require a new migration.
- Dates currently default to UTC, consistent with existing app behavior. Shop
  timezone and actual appointment/application times remain part of later work.
- Property measurement and note histories display the latest 100 records while
  retaining older rows. Pagination/export improvements remain future work.
- Presets currently prepare service names/descriptions; new billing, checklist,
  field-selection and job capabilities are not yet connected to them.
- Private photo implementation must include storage access, quotas, image
  validation, tenant isolation and coordinated backup/restore before activation.
  No storage account was purchased or configured during these slices.
- QA scripts and screenshots remain under `/tmp/sideyard-qa/` (`business.cjs`,
  `property.cjs`, `vehicles.cjs`, `notes.cjs`). They use synthetic local owners or
  isolated staging demos, never real customer records.
- Owned local QA servers and disposable Postgres container were stopped and
  removed after verification. Build caches remain reusable. Start each full
  integration-suite run with a fresh database to avoid legacy fixture collisions.
