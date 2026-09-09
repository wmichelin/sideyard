# Sideyard delivery status

Status: verified — nine foundational slices in production; standalone jobs and the badge fix verified on staging. Overall execution plan remains in progress.
Last completed: approved production promotion, standalone job/site-survey workflow and single-line status badges.
Now: durable handoff and staging review.
Next: calendar integration, appointments and remaining scheduling work; new production promotion only after approval.
Staging: https://clubdue-staging.michelindigitalmedia.com (verified through standalone jobs and the badge fix).
Blocker: none for continued development. Production requires review and approval.

The user explicitly authorized continued building on September 8, 2026, and
production promotion through visit-linked notes on September 9. The earlier
planning-only status in the execution/requirements documents describes the
original handoff; this file tracks subsequent implementation. New standalone
jobs and the status-badge fix are a separate staging review after that promotion.

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
linked directly to standalone jobs and staff permissions remain. Existing recurring
and seasonal visit notes are implemented in the next slice below. Vehicle/equipment histories
are implemented in the next slice below. Private
property photos are verified on staging.
Billing, scheduling extensions and provider integrations remain as planned. No
paid integrations or live customer payment collection are configured by this change.

## Continuation details

- Migrations 021–030 are committed and applied on staging; production is through
  029. Migration 030 adds jobs and their history. Do not edit committed migrations'
  checksummed contents; future changes require a new migration.
- Vehicle/equipment event notes default to the browser's date/timezone, with an
  editable UTC fallback without JavaScript. Time is optional and remains unknown
  when blank. Older property notes remain date-only; shop timezone and actual
  appointment/application times remain part of later work.
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
- Equipment private details are current editable information; the new asset
  history slice adds a separate authored service-event timeline. Address correction changes the
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

## Private photo staging artifact

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

## Vehicle and equipment service histories

- Each vehicle/equipment record now links to a separate internal service
  history. Entries keep event date, optional actual time, event timezone, author
  and recording time. Unknown time stays unknown; corrections preserve earlier
  entries. Current standing asset details remain independently editable.
- Owner-only routes and tenant-safe parent constraints; concurrent identical
  retries acknowledge one note, while changed or cross-asset submissions fail.
  Histories show the latest 100 entries and retain all earlier rows.
- Migration 028 and embedded timezone data support the scratch image. Invalid
  local clock times are rejected; repeated times require an explicit occurrence.
  A slow default script preserves date/timezone fields already changed by a user.
- Full Go/Postgres tests, vet, static build, both operational rehearsals and local
  browser checks passed. The backup rehearsal proves exact timestamp/timezone/
  author/body restoration. See [asset history delivery](SIDEYARD-ASSET-HISTORY-DELIVERY.md)
  for review, regression evidence and staging verification.
- QA script: `/tmp/sideyard-qa/history.cjs`; supports local owner or isolated
  staging demo. Covers two vehicles, equipment, corrections, DST handling,
  delayed JavaScript, no-JavaScript fallback and 390/1440-pixel views.
- Both local history QA servers and the task-owned disposable database were
  removed. Scripts/screenshots and build caches remain available.
- Remaining E2 work includes customer-linked notes, property event-time
  upgrades and vehicle/equipment photo associations. E3 adds jobs, finite
  programs and crew scheduling; E4 adds configurable contract billing across
  trades. Four/five treatments are one use case, not a universal billing rule.

## Asset-history staging artifact

- Source: `ea9d91d9f6892a8b1a7d036429081a819eed3e82`.
- Image: `ghcr.io/wmichelin/clubdue@sha256:6567c243466dc954ece39c91d5a2210c73879b63fee9ec359b2c80d92a751e32`.
- CI `34314814217` and staging `34314814203` succeeded. Manual smoke and
  390/1440-pixel browser acceptance passed, including the deliberately delayed
  script regression. No browser errors, overflow or unexpected staging errors.
- Running staging image/revision verified directly. Production remains on
  `7d33ab7ff1c05a796bd351b647f81eea58729726` and its gate is false. No promotion.

## Visit-linked notes

- Owner-only **Visit notes** links on recurring and seasonal Service work rows.
  Each existing visit keeps its own dated/timed internal history, with authorship,
  immutable corrections and safe duplicate retries. Completed visits accept
  follow-up notes. Rescheduling retains the same visit/history identity.
- Adding a note does not change work status, create visits or consume included
  services. Private notes stay out of the established CSV exports; existing work
  outcome and legacy export-note behavior remains unchanged.
- Shared event-time validation and form rendering also serve vehicle/equipment
  histories. Unknown event times remain blank; DST ambiguity is explicit.
- Migration 029 has tenant-safe recurring/seasonal foreign keys and exactly one
  parent per note. The app can roll back while retaining new history rows.
- Full Go/Postgres tests, vet, static build, both operational rehearsals and
  local visit/asset browser checks passed. See
  [visit history delivery](SIDEYARD-VISIT-HISTORY-DELIVERY.md) for review and evidence.
- QA scripts: `/tmp/sideyard-qa/visit-history.cjs` and existing `history.cjs`;
  screenshots retained there. Local test server/database were removed.
- Next: customer history and property event-time upgrades, followed by standalone
  jobs/site surveys, finite service programs, appointments/crew assignment and
  configurable contract billing. No new payment or billing integration yet.

## Visit-note staging artifact

- Source: `ae6f16ae9f9feaf52bf58093cf1f0c3b8c7a7c34`.
- Image: `ghcr.io/wmichelin/clubdue@sha256:a57a268f081b8f2c96550eb2d61b00e36faae855757662c0a62b27162f5d319c`.
- CI `34353916568` and staging `34353916405` succeeded. Manual smoke and both
  visit/asset history browser flows passed at 390/1440 pixels. No browser errors,
  overflow or unexpected staging log errors. Running source/image verified.
- Production remains on `7d33ab7ff1c05a796bd351b647f81eea58729726`; gate false.
  The user's “Do it” was ambiguous between promotion and further development;
  clarification was requested and standing build authorization used to continue.
  No production approval was assumed. See the visit delivery artifact for the
  exact earlier reviewed release if that is the one they approve.

## Approved production release — September 9

- User explicitly requested production promotion followed by the next feature.
- Reviewed source: `ae6f16ae9f9feaf52bf58093cf1f0c3b8c7a7c34`.
- Image: `ghcr.io/wmichelin/clubdue@sha256:a57a268f081b8f2c96550eb2d61b00e36faae855757662c0a62b27162f5d319c`.
- [Production workflow](https://github.com/wmichelin/clubdue/actions/runs/34354641834)
  succeeded, including authenticated smoke. The running production revision and
  digest matched. The 390/1440-pixel visit-history browser workflow passed on
  getsideyard.com; the legacy ClubDue demo/dashboard remained accessible.
- Production logs had no unexpected errors. Gate returned to false immediately
  after successful promotion. All nine foundational slices are now in production.
- No new production approval is assumed for the subsequent standalone-job and
  badge-fix build; that work is staged separately.

## Standalone jobs and site surveys

- Customer-owned one-time service jobs and estimating/site-survey visits,
  optional same-customer property and optional planned day. No annual agreement
  required or generated. Owner Jobs view has open/all filters; customer records
  link directly to job creation.
- Editable open jobs with revision protection. Explicit complete/cancel/reopen
  actions retain authored snapshots of every change. Concurrent create retries
  deduplicate even after later edits; changed submissions cannot replace jobs.
- Jobs, history, outcomes and properties are tenant-scoped. Completed/cancelled
  jobs must reopen before editing; deleting a customer cannot silently erase
  their work history. Whole-shop/demo cleanup still cascades correctly.
- New internal/jobs domain and migration 030. Existing recurring/seasonal
  schedules and entitlements remain independent. Current planned days appear in
  Jobs; appointment times, calendar integration and crew assignment remain next.
- Screenshot-reported status badges now stay on one line; the mobile work-row
  layout reserves enough space for the full status label.
- Full Go/Postgres tests, static build, vet, backup/restore and pilot rehearsals
  passed. Local phone/desktop job flow and badge checks from 320–1440 pixels
  passed. See [job delivery](SIDEYARD-JOBS-DELIVERY.md) for review and evidence.
- Local test server/database removed; QA scripts/screenshots remain under
  `/tmp/sideyard-qa/` as `jobs.cjs` and `status-badges.cjs`.
- Remaining E3 work: links to existing recurring visits, calendar/appointments,
  treatment programs, crew/checklists and job asset/photo associations. E2
  customer/property timeline finish and E4 flexible quote/billing work remain.

## Current verified staging artifact

- Source: `53b7878955e9eac7f3288e926218dfdbd612c427`.
- Image: `ghcr.io/wmichelin/clubdue@sha256:61d6c07c1fc88becb1a69e09f170a9ec94eb4857509ec438a1eb5f37eb6b584a`.
- CI `34356135588` and staging `34356135565` succeeded. Manual smoke, standalone
  job browser acceptance and status-badge checks passed. Jobs tested at 390/1440
  pixels; all 90 work statuses checked at six widths from 320 to 1440 pixels.
- No browser errors, document overflow or unexpected staging log errors. Running
  revision/digest verified. Production remains at the explicitly approved visit
  history source `ae6f16ae9f9feaf52bf58093cf1f0c3b8c7a7c34`; gate false.
- Try **Jobs → New job**, or **New job or site survey** from a customer record.
## Azure migration — implementation started September 9, 2026

- User approved the [Azure plan](SIDEYARD-AZURE-PLAN.md): SQL serverless
  0.5–1 vCore with free-limit pause, private versioned Hot LRS Blob storage and
  **seven-day backups**. This replaces the proposal's 30-day recovery target.
- Status: implementing. Azure CLI authenticated to enabled `Azure subscription 1`
  (`ddd8d30e-b827-461d-bf25-09f77337cbdd`), actual `FreeTrial_2014-09-01` offer
  with spending limit On. No billing upgrade or paid SQL overage was enabled.
- Terraform state account `sideyardstateddd8d30e` is private/versioned in East US;
  bootstrap applied and its second plan was empty. Staging environment state is
  remote under its own key. Bootstrap state was also migrated to its own remote
  key and its five resources are accessible from remote state.
- Central US supports the requested SQL SKU in this subscription; East US was
  restricted. Staging private versioned Hot LRS account `sideyardstgddd8d30e`,
  `photos`/`backups` containers, SQL logical server and owner action group exist.
  The first SQL database create rejected the proposed 15-minute idle delay:
  free-limit AutoPause requires its **60-minute default**. User informed; plan
  corrected without enabling paid overages. Live readback confirms min 0.5/max 1,
  free-limit AutoPause, 32 GB maximum, Local redundancy and seven-day native
  retention. Two enabled compute quota alerts exist. The native earliest restore
  date is still initializing; no native restore is claimed.
- Local Blob integration retains full PostgreSQL recovery bytes. Focused real-PG
  tests passed for concurrent retry, hidden/quota-counted pending uploads, existing
  photo visibility during failed backfill, cross-tenant rejection, exact-version
  read, deletion during upload and customer-cascade retirement. CLI `sync-photos`
  provides bounded backfill/resume. No version garbage collection is enabled.
- Real staging Azure photo handoff passed locally with a synthetic fixture:
  exact-version read, second batch with no work, and deletion without resurrection.
- Staging seven-day encrypted export service completed at `2026-09-09T21:57:58Z`.
  Its authenticated manifest and exact versions were downloaded from Azure and
  restored into `sideyard-azure-restore-check` with no network or durable mounts:
  2 shops, 31 customers, 0 photos and 30 migrations. Separate production-mode
  rehearsal covers photo recovery; no actual staging-photo restore is claimed yet.
  Daily backup/hourly freshness timers are enabled; separate backup identity has
  no photo/delete access. Staging's final Terraform plan was empty.
- Full real-Postgres Go suite, vet and static build passed; latest production
  backup/restore rehearsal passed. Six backup-auth tests, seven Azure export
  tests and two backup-monitor tests passed. Read-only follow-up reviews found
  no remaining Blob staging blockers; their operator-doc correction was included.
- Azure Monitor's synthetic test endpoint returned `Conflict: Free subscription
  not supported`. Quota rules are configured but synthetic Azure email delivery
  is unverified. Backup failure/staleness notifications use the existing Resend
  integration; independent monitoring for complete droplet loss remains pending.
- Blob app source `90bcf58f1fa34faac1733ecf5ecc89a4539c94d5`: CI
  `34410242419` and staging `34410242364` succeeded. Immutable image
  `ghcr.io/wmichelin/clubdue@sha256:c0e694f9bac08e1277d6898b3786654a3cbb231f04f9d2575ac66725e166b188`
  is running on staging with Azure photo configuration; revision/digest/logs and
  migration 031 verified. Manual smoke and desktop/mobile (390/1440) real-browser
  photo upload, large proxy upload, private/foreign/demo access and deletion passed.
  `sync-photos` on the deployed container completed with zero pending handoffs.
- Production Azure SQL/Blob resources and two quota alerts applied successfully;
  free-limit flags, 0.5–1 vCore, 60-minute idle delay and seven-day retention read
  back. The main production app is still on its previously approved release and
  PostgreSQL. No SQL Server application port or production Blob cutover is claimed.
- Production encrypted Azure export completed `2026-09-09T22:06:41Z`. Daily
  export and hourly/OnFailure monitor units are installed for both environments.
  A direct backup test email was accepted by Resend. Initial Python default
  User-Agent received HTTP 403; identifying requests as `Sideyard/1.0` fixed it,
  matching the app's existing notification client.
- Staging: https://clubdue-staging.michelindigitalmedia.com (verified for Azure Blob; SQL application port pending).
- Three read-only specialist agents reviewed all six delivery perspectives.
  Product/QA: retain tenant/owner photo access, reservations and tombstones;
  prove oldest retained backup with an old photo deleted recently. Architect/
  staff: 253 SQL call sites and 31 tables require an explicit SQL Server port;
  preserve filtered uniqueness, triggers, relationships and PostgreSQL recovery.
  DevOps/testing: own free flags and seven-day backup policy explicitly in
  Terraform; prove pause/resume, exact object-version recovery and real alerts.
- Backup review found that the existing OpenSSL `key:file:/dev/fd/3` argument
  uses a literal HMAC key instead of reading the intended secret. New versioned
  helper authenticates ciphertext with the real secret; six security tests and
  a complete production backup/restore rehearsal passed locally.
  Historical sidecars require deliberate legacy recovery; no automatic downgrade.
- Blob versions must be retained from retirement/reference expiry, not original
  creation time. Seven-day application recovery plus soft-delete retention can
  keep billable bytes longer. Customer/shop cascades must not erase cleanup data.
- Execution order: infrastructure and authenticated recovery, Blob integration,
  explicit SQL Server domain port, isolated migration/restore and staging soak,
  then reviewed production cutover under the existing release workflow.

### Azure Blob final staging verification

- Current staging source `7181040659452d11b966e755efb8cd44502f748d`; CI
  [34410708421](https://github.com/wmichelin/clubdue/actions/runs/34410708421) and staging
  [34410708450](https://github.com/wmichelin/clubdue/actions/runs/34410708450) succeeded.
  Running revision and image verified:
  `ghcr.io/wmichelin/clubdue@sha256:61834ce74a7bd52f77b14aca8d388d2c6e68a1caf86ac7fd691515f36b029c35`.
  Final manual staging smoke passed. Go application code is unchanged from the
  `90bcf58` mobile/desktop Azure photo acceptance run above.
- Downloaded the latest encrypted exports and their authenticated exact-version
  manifests from each Azure account. Restored into isolated, network-disabled
  PostgreSQL containers using temporary memory-backed data directories. Staging:
  9 shops, 220 customers, 2 photos, 31 migrations. Production: 6 shops, 152 customers,
  0 photos, 29 migrations. The active Azure-backed staging photo's retained
  database bytes matched its SHA-256 hash after restore. Synthetic demo/QA rows
  explain staging counts; no customer record contents are included in this log.
- Native Azure SQL restore windows now report earliest restore dates:
  staging `2026-09-09T22:02:16Z`, production `2026-09-09T22:16:12Z`.
  Both retention policies are seven days. This confirms available restore
  history, not a completed native point-in-time restore into another database.
- A real TLS-verified Entra SQL connection to staging returned database
  `sideyard`, engine edition 5. Closing the query left zero idle connections.
  This was a read-only connection probe; no application schema or data was moved.
- Production's separate, container-scoped photo identity is provisioned, and
  its protected configuration is prepared for the reviewed app promotion.
  Current production app and database backend remain unchanged. Promoting this
  staging artifact also includes the previously staged jobs and status-badge fix;
  user review must cover that complete artifact before production deployment.

### SQL Server connection foundation

- Replaced the app/domain dependency on the concrete pgx pool with the small
  `internal/database` connection/transaction interfaces. PostgreSQL's native
  migration engine and all 31 migration files remain unchanged. The wrapper
  normalizes missing-row errors consistently, including direct import comparisons.
- Added a lazy SQL Server adapter using Microsoft's Go driver 1.9.3 (compatible
  with the existing Go 1.23 toolchain). Azure construction uses an injected,
  persistent Entra credential and verified TLS 1.2+, retains no idle sessions,
  and performs no startup query, migration or automatic statement retries.
- Separate checksummed T-SQL migrator reserves one session for its lock and all
  transactions. It checks negative lock status codes, rolls back failed DDL and
  force-discards the session during cleanup so an unlock failure cannot pass a
  held lock to a queued pool caller.
- Product/QA review: keep SQL selection disabled and preserve user workflows;
  test row/value fidelity, cancellation and no false success. Architect/staff:
  preserve native PostgreSQL migrations and native statements, use explicit UUID
  projection casts, and port the calendar to SQL snapshot isolation before use.
  DevOps/testing: long-lived token credential, real-engine CI with required flag,
  isolated fixtures, single-session migration ownership and forced cleanup.
  All concrete review corrections were included.
- Focused PostgreSQL/SQL Server tests passed: missing rows and driver errors,
  affected rows, exact decimal/date/Unicode/16-byte image round trips, snapshot
  consistency, cancellation/rollback, zero retained SQL connections, concurrent
  migrations, checksums, failed DDL and canceled lock recovery. The complete
  Go/PostgreSQL suite, vet, static build, production backup/restore rehearsal,
  pilot-onboarding rehearsal and standalone SQL Server rehearsal passed locally.
  One initial full run reused a database containing a fixed-token fixture from
  an earlier test; a fresh disposable database resolved that fixture collision.
- The new CI job runs `scripts/sqlserver-rehearsal.sh` against a pinned local
  SQL Server 2022 Developer image. Public dependencies are built before the
  isolated engine network is created, so tests need no external network access.
- Runtime SQL Server selection, native application schema/queries, data transfer,
  SQL export/recovery and actual Azure pause/resume remain unfinished. Both app
  environments still use PostgreSQL. No new production release is claimed.
