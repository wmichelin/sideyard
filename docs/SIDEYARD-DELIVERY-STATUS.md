# Sideyard delivery status

Status: verified — seven foundational slices on staging. Overall execution plan remains in progress.
Last completed: private property photos verified on staging, including large uploads and deletion.
Now: durable handoff and staging review.
Next: expanded internal timelines and scheduling; production promotion only after approval.
Staging: https://clubdue-staging.michelindigitalmedia.com (verified through private property photos).
Blocker: none for continued development. Production requires review and approval.

The user explicitly authorized continued building on September 8, 2026. The
earlier planning-only status in the execution/requirements documents describes
their original handoff; this file tracks subsequent implementation. Production
promotion has not been authorized for these new features.

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
- Dated-note source: `84b7340ab623375c6589770f9905b40e1bf20eb1`; CI `34298057779`
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
dated property notes are also implemented. Equipment editing and address correction are now implemented. Authored notes
linked directly to jobs/vehicles/equipment and staff permissions remain. Private
property photos are verified on staging.
Billing, scheduling extensions and provider integrations remain as planned. No
paid integrations or live customer payment collection are configured by this change.

## Continuation details

- Migrations 021–027 are committed and applied on staging. Do not edit their
  checksummed contents; future changes require a new migration.
- Dates currently default to UTC, consistent with existing app behavior. Shop
  timezone and actual appointment/application times remain part of later work.
- Property measurement and note histories display the latest 100 records while
  retaining older rows. Pagination/export improvements remain future work.
- Presets currently prepare service names/descriptions; new billing, checklist,
  field-selection and job capabilities are not yet connected to them.
- Private property photos use bounded Postgres storage with private access,
  quotas, validated/normalized images and coordinated backup/restore. No separate
  storage account was purchased. Object storage remains the media scaling path.
- QA scripts and screenshots remain under `/tmp/sideyard-qa/` (`business.cjs`,
  `property.cjs`, `vehicles.cjs`, `notes.cjs`). They use synthetic local owners or
  isolated staging demos, never real customer records.
- Owned local QA servers and disposable Postgres container were stopped and
  removed after verification. Build caches remain reusable. Start each full
  integration-suite run with a fresh database to avoid legacy fixture collisions.

## Equipment and address continuation

- Equipment records: installation/warranty dates, serial/reference, property
  location and private service details, with owner-only editing and stale-save
  protection. Existing equipment IDs and installation dates remain intact.
  See [equipment delivery](SIDEYARD-EQUIPMENT-DELIVERY.md) for full review/evidence.
- Equipment source `33af03206bb5b1759aa46b97293a77f5c2e4b98d`; CI
  `34303050158` and staging `34303050208` succeeded. Manual smoke, phone/desktop
  browser flow and running revision verification passed.
- Address correction preserves property identity and attached history. Database
  revision tracking also covers the existing CSV import path. Unit/suite now
  appears in customer property summaries. See
  [address delivery](SIDEYARD-ADDRESS-DELIVERY.md) for review and local evidence.
- Address source `89643542259fc6947e9e385bcf49cb8a3df47303`; CI
  `34303519623` and staging `34303519617` succeeded. Manual smoke and staging
  phone/desktop walkthrough passed. Both new slices passed full fresh-Postgres tests, vet, static build,
  production backup/restore and pilot rehearsals, and local phone/desktop checks.
- Equipment/date/address forms retain entered values on validation errors or
  conflicts. Unknown dates and optional unit remain blank when cleared.
- Equipment private details are current editable information; they are not yet
  an authored equipment service-event timeline. Address correction changes the
  current address displayed by existing work/export views. Historical quote and
  contract address snapshots remain part of E4.
- QA scripts: `/tmp/sideyard-qa/equipment.cjs` and `address.cjs`. Equipment uses
  argument `staging` for its synthetic public-demo flow; address uses `demo`.

## Address correction staging artifact

- Source: `89643542259fc6947e9e385bcf49cb8a3df47303`.
- Image: `ghcr.io/wmichelin/clubdue@sha256:a9efde717b2114e8a1477ab77e451e04b6b529f01e62d57101f51f40948c4db9`.
- Both values read directly from the running staging container after verification.
- Production remains on the approved signup-notification source `7d33ab7`; the
  production gate is false. Do not promote without explicit approval in chat.
- Two local QA servers (`/tmp/sideyard-equipment-qa`, `/tmp/sideyard-address-qa`)
  and the disposable `sideyard-equipment-test-db` container are cleaned up after
  this checkpoint. QA scripts/screenshots and build caches remain available.

## Private photo slice

- Owner-only property photo upload/view/delete; before, after, damage, access and
  general categories, captions and upload authorship. No public image URLs.
- Normalized JPEG/PNG with metadata removal and JPEG orientation handling; HEIC
  has an explicit conversion fallback. Input dimensions/bytes, stored bytes and
  per-shop storage are bounded. Decode admission limits simultaneous processing.
- 100 active photos / 50 MiB per shop; 5 MiB upload / 4 MiB normalized image.
  Captions remain on ordinary validation failures, retries deduplicate, and
  deletion tombstones prevent old forms from resurrecting deleted photos.
- Postgres-backed bytes and metadata are an explicit bounded-pilot storage choice;
  see [photo review and evidence](SIDEYARD-PHOTOS-DELIVERY.md). Photos share the
  encrypted database backup/restore. Separate object storage is a scaling step.
- Live image data/caption/author are removed on deletion. Encrypted backups can
  retain previous content until retention expires; historical recovery restores
  the state at its recovery point.
- Migration 027, full Go tests/vet/static build, both rehearsals and local browser
  checks passed. The production rehearsal now checks actual image byte/caption/
  author restoration and upload retry. Public demo uploads remain disabled.
- Deployment helper adds an 8 MiB request envelope only beside its unique managed
  Sideyard upstream; larger phone uploads must pass the real staging proxy test.
- Initial source `0d2eabec2c941eb9ba8bf660255ee64c322eb50b`; CI `34307036176`
  and staging `34307036167` passed, but the larger staging browser upload failed
  because the scratch image lacked `/tmp`. Follow-up `1df43d3` adds it; the
  expanded >2 MiB container/backup rehearsal passed. Final staging recheck also
  passed; see the verified artifact below. No production promotion.

## Current verified staging artifact

- Source: `1df43d3dd4140141ad3f05e8fd6e93876c8627a5`.
- Image: `ghcr.io/wmichelin/clubdue@sha256:b178cdb5312fc5e20d1cdb10fa4af9853d77e3648373d0dde02bec871bed79aa`.
- CI `34307354003` and staging `34307353974` succeeded. Manual smoke passed;
  390/1440-pixel owner browser flow passed large upload, unsupported-file feedback,
  private access, other-tenant denial and delete/re-upload. Demo uploads remain
  disabled. No browser errors, overflow or unexpected staging log errors.
- Both artifact values were read from the running staging container. Production
  remains on `7d33ab7` and its deployment gate is false.
- The temporary staging QA tenant and credentials were removed. Local photo QA
  server and disposable database were removed; screenshots/scripts remain.
- Next: finish authored timelines across assets/visits with actual event times,
  then the E3 job/program/crew model and E4 flexible contract billing. Trade
  presets remain defaults; billing is not restricted to lawn treatments.
- Photos currently attach to properties. Vehicle/equipment/job associations and
  crew access remain; separate object storage is the scaling path beyond the
  bounded database-backed pilot. Owners can try photos from their own staging
  customer/property record, not through public-demo uploads.
