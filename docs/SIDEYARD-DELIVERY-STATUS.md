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

- Connection foundation source `365d511b697ac5af6ddaf81762181b70670a0831`
  passed CI [34413139882](https://github.com/wmichelin/clubdue/actions/runs/34413139882)
  and staging [34413139702](https://github.com/wmichelin/clubdue/actions/runs/34413139702).
  Running image/revision verified:
  `ghcr.io/wmichelin/clubdue@sha256:af0a9a7f568b17d41b4fbdcd62524183528c212a861fbe37d6dc8fa084782731`.
  Manual staging smoke passed. Production approval remains pending for the
  earlier reviewed Blob release `7181040`; no approval is inferred for later code.

### Native interest/outbox and idle worker — implementation

- Added native SQL Server signup/outbox schema and explicit T-SQL queries.
  Signups and alerts commit together; concurrent duplicate emails retain the
  original details and enqueue once. Claim locks work with SQL Server's
  read-committed snapshot setting enabled. UUID projections preserve the same
  lowercase provider idempotency keys used by PostgreSQL.
- Real-engine tests share retry/acknowledgement and competing-worker scenarios
  across PostgreSQL and SQL Server. Additional SQL tests verify concurrent save,
  failed queue insertion rollback, after-commit wakes, supplementary Unicode at
  current limits, and rejection of a mixed-width 2,001-character message. Review
  caught SQL Server's 8,000-byte concatenation truncation; an explicit large-value
  cast preserves the sentinel-based character count.
- Replaced perpetual empty-queue polling with a buffered post-commit wake,
  startup recovery, durable next-attempt timers and daily maintenance reconciliation.
  Tests verify the empty-query/wait race, bursts beyond the 20-item batch limit,
  no idle polling, bounded lock contention, interrupted retry waits and database
  failure backoff. Worker shutdown follows HTTP drain. Lost process handoffs may
  wait for reconciliation; no unconditional exactly-once delivery is claimed.
- Full Go suite with both real engines, vet, static build and the expanded
  isolated SQL rehearsal passed locally. Native application SQL selection remains
  disabled. Next: remaining tenant/auth and operational domains, migration/export
  tooling, cold-start behavior and actual Azure idle/resume proof.
- Staging: https://clubdue-staging.michelindigitalmedia.com (verified foundation;
  native interest/worker deployment verification recorded below).
- A real two-instance SQL test now covers a last HTTP signup committing during
  shutdown after the surviving worker already observed an empty queue. The row
  remains durable and the reconciliation wake delivers/acknowledges it once.
- Azure staging SQL was observed `Paused` via the management API. A subsequent
  TLS/Entra read-only probe starting `2026-09-09T23:00:22Z` succeeded after three
  connection attempts in **45.064 seconds**, ending with zero open/idle sessions.
  Only database-unavailable error 40613 was retried; no writes were involved.
  This verifies resource pause/resume, not the app's cold-start experience.
  The current 20-second HTTP response timeout remains a cutover blocker.
- Before SQL activation, resolve repeated hourly mail failures and overlapping
  retained-container maintenance: these can prevent a 60-minute idle window and
  exhaust the free quota. Empty-queue sleep alone is not proof of quota fit.

- Interest/worker source `b0ebaf228d5353f9b4a86633714fb7318347ffed` passed
  all four CI jobs [34415123296](https://github.com/wmichelin/clubdue/actions/runs/34415123296)
  and staging [34415123322](https://github.com/wmichelin/clubdue/actions/runs/34415123322).
  Running source and immutable artifact were independently read back:
  `ghcr.io/wmichelin/clubdue@sha256:407b4b8590eebd4fa8a47fd1c0c505ae69980e6b5d788ee2722d28f1c893aa00`.
  Manual staging smoke passed all public/demo/import/work/renewal checks. Resend
  is not configured on staging; native queue delivery/recovery is proven by
  isolated real-engine tests, not a live staging email. Production is unchanged.

### Native tenant/auth schema — implementation

- Added separate SQL Server shops/users/sessions, owner recovery audit and pilot
  activation tables plus explicit native authentication and milestone statements.
  PostgreSQL queries and migrations retain their current behavior. Backend
  selection remains disabled until all operational domains and migration pass.
- Product/QA: preserve owner-only recovery, existing tenant, independent device
  sessions, expiry and current text limits. Architect/staff: hash-indexed MAX
  identities require full-value and byte-length comparisons, transaction-owned
  locks, lowercase UUID projections and explicit derived canonical email.
  DevOps/testing: initialize computed-index SET options on fresh and reused
  driver sessions, and close an unowned connection if initialization fails.
- Review and a real PostgreSQL/SQL Server comparison exposed incompatible Unicode
  LOWER mappings. SQL Server stores Go-normalized email separately; historical
  migration must preserve original text and validate canonical identity both
  before and after transfer. This accepts one repository-owned derived invariant
  to preserve the existing external identity contract. Digest collisions fail
  writes instead of allowing cross-account recovery. No historical rows have
  been transferred yet.
- Focused SQL/PG owner isolation, same-name and same-email races, Unicode/long
  identities, historical case/space distinctions, independent device sessions,
  exact demo expiry, staff-to-owner recovery and tenant cascade isolation pass.
  Failed provisioning/recovery audit writes roll back all preceding changes.
  Closed-value columns reserve one extra byte so SQL cannot trim trailing spaces
  before constraints; tests reject those padded values and invalid account text.
- Full Go suite with both real engines, vet and static build passed. Review-driven
  additions received targeted real-engine checks. The isolated SQL rehearsal
  passed. An initial full run exposed a test double missing its new Dialect method;
  the fixture was corrected and the full suite rerun successfully.
- Production backup/restore and pilot-onboarding rehearsals also passed. These
  use isolated local containers and do not promote the production application.
  Staging remains verified at `b0ebaf2`; tenant/auth slice awaits staging delivery.

- Tenant/auth source `fcb47f73087f9b867bb1597d6e7ffe888ca86956` deployed to
  staging [34416609502](https://github.com/wmichelin/clubdue/actions/runs/34416609502),
  but CI [34416609579](https://github.com/wmichelin/clubdue/actions/runs/34416609579)
  found a canceled native dial returning a typed-nil driver connection. Cleanup
  attempted to close it and panicked. This dormant SQL path is a release blocker;
  staging deployment alone is not verification. PostgreSQL app traffic is unaffected.
- The fix normalizes that typed nil before cleanup. A direct regression and
  twenty repetitions of the real SQL shutdown/last-signup scenario passed locally.
  CI and staging must pass again before this source is eligible for review.

- Corrected source `341e647f17d93bd917fa94613938e87ebaf17479` passed all CI jobs
  [34417010665](https://github.com/wmichelin/clubdue/actions/runs/34417010665) and
  staging [34417010633](https://github.com/wmichelin/clubdue/actions/runs/34417010633).
  Independent revision/image readback and manual staging smoke passed:
  `ghcr.io/wmichelin/clubdue@sha256:3155a4769d42618f1ae3d0c5b0f6e8d09c3af32fe5fcf90e88529de4bec8e835`.
  Production remains unchanged; the latest code is not approved for promotion.

### Connection resume and availability — implementation

- Azure connections retry only initial error 40613, with bounded 1/2/4/8/10-second
  waits within an 80-second connection budget. No application statement or started
  transaction is replayed, and 40613 alone is not labeled quota exhaustion.
- DevOps/architect review found native prelogin/TLS IO can ignore context and lose
  a raw socket before constructing its SQL connection. Each attempt now owns its
  transports until native login/session initialization succeeds. Cancellation and
  errors close them; successful ownership transfer stops the cancellation watcher.
  A silent native prelogin test verifies 25ms cancellation despite the driver's
  two-second socket timeout and observes peer closure. Real-engine tests verify
  normal connection/session/migration behavior still works.
- Product/QA: public landing, sign-in, enrollment, demo entry and assets remain
  database-free on GET even with old cookies. Existing owner/demo recognition
  remains in the protected POST. Authentication dependency failures return 503,
  preserve session cookies and form identity fields, and do not charge the
  invalid-password throttle. Protected demo admission fails closed on lookup
  failures. Validation errors remain separate; passwords are not redisplayed.
- Staff/testing: explicit error classification, injected failing pool/native
  connector/wait seams; verify no false logout, account creation or successful
  save during outages. Actual Azure application cold start remains unverified:
  request read/write/proxy budgets, lazy startup, background ownership, the full
  domain port and transfer/restore gates are still unfinished.
- Availability tests cover login and enrollment outages, credential throttling,
  public GET/CSRF admission without database IO, demo lookup failure, and an
  existing owner's CSRF-protected demo POST preserving that account. The full
  app/PostgreSQL suite passed. Native connector tests passed after correcting a
  test's assumption about the driver's query interface; vet and static build pass.
  A real successful SQL connection remains usable with a fresh query context
  after its original Connect context is canceled.
- Production backup/restore and pilot rehearsals passed. A local SQL rehearsal
  encountered a transient module-download timeout; compiler caches are now reused
  without changing the isolated engine network. The complete SQL rehearsal is
  rerun passed after the final transport correction. Product graph renders 36 nodes and
  64 edges; local links and catalog coverage pass. Availability changes are pending
  staging verification; the latest verified staging source is `341e647`.

- Availability source `66b5298469fcd0e41b41d9d3ee4047afc62a79a8` passed all CI
  [34418067325](https://github.com/wmichelin/clubdue/actions/runs/34418067325) and
  staging [34418067362](https://github.com/wmichelin/clubdue/actions/runs/34418067362).
  Verified running source/image:
  `ghcr.io/wmichelin/clubdue@sha256:c6440e96f3f6b5775ee185d162c20f0b275dd9ff2c47689e47c9ebed372e76ec`.
  Manual staging smoke passed. Browser checks at 390/1440px passed sign-in,
  database-free login entry, owner-preserving demo entry, customer book and closed
  enrollment with no browser errors. Outage simulation remains an isolated test,
  not a disruption of staging's database. Production remains unchanged.

### Native text capacity correction

- Review extended the trailing-space constraint check to supplementary Unicode.
  Native interest fields at their physical UTF-16 capacity could discard one
  trailing space before validation. Append-only migration 003 preserves complete
  nonindexed values with MAX storage and gives indexed email one extra code unit.
  Product rune limits, BIN2 email identity, defaults and unique index remain.
- SQL Server requires dropping/recreating dependent CHECK/default constraints and
  the email index within the migration transaction before widening columns.
  Fresh-schema and 001+002 upgrade tests pass: all exact-limit supplementary
  values survive, every limit-plus-space value fails, defaults and uniqueness
  remain intact. Existing SQL migrations/checksums are unchanged. The full native
  SQL app/adapter suite passed; application SQL selection is still disabled.


- Capacity correction source `8346acec0b7146a276c0704c9fea4db96bcbd4c5` passed CI
  [34418590986](https://github.com/wmichelin/clubdue/actions/runs/34418590986) and
  staging [34418590945](https://github.com/wmichelin/clubdue/actions/runs/34418590945).
  Running revision/image readback and manual staging smoke passed:
  `ghcr.io/wmichelin/clubdue@sha256:22ba461561be88fd43f2b5610197c136a578cdc1adfc33238092acdd3d0ac563`.
  The application still uses PostgreSQL; production is unchanged.

### Native customer-book schema — implementation

- Separate native migrations 004/005 add customers, properties, equipment and
  customer vehicles, with explicit native CRUD and address revision statements.
  Historical unbounded text remains unbounded; exact nullable external identities
  use filtered hash indexes. Tenant ownership follows one cascading path. Before
  importing, audit legacy cross-tenant property relationships and date ranges.
- Architect/staff review: preserve optional whitespace, reject UUID-prefix aliases,
  and compare address changes by binary content plus byte length. SQL's AFTER
  trigger increments revisions only for actual changes, including multirow writes.
  Later dependent tables must restore deletion restrictions before runtime activation.
- Product/QA review: preserve separate assets, owner/staff visibility, existing
  text limits and optimistic edits. Malformed equipment/address paths return 404;
  malformed vehicle form IDs return 400 without querying the database.
- Unit-testing review: share vehicle/equipment behavior across both engines; verify
  foreign-tenant reads/writes, exact long Unicode text, duplicate identities,
  concurrent address edits, rollback on revision overflow and four simultaneous
  customer-detail loads. All pass. The latter proves property rows are closed
  before borrowing equipment connections, fixing potential pool exhaustion.
- DevOps sequencing: keep SQL runtime selection disabled and PostgreSQL migration
  checksums unchanged. The full suite with both real engines, vet and static build
  passed; isolated SQL Server rehearsal and focused review additions passed.
  CI and staging verification are the next gates for this slice.
- Remaining HTTP activation gates include customer reads that currently mask
  dependency errors and property/equipment create handlers that can redirect after
  a failed write. Those older paths need explicit unavailable/unsaved responses
  before cold-start SQL traffic is enabled. No live Azure data has been transferred.
- Staging: https://clubdue-staging.michelindigitalmedia.com (verified at `8346ace`;
  this customer-book slice is pending deployment). Production remains unchanged.


- Customer-book source `9246dea71c8e2b7cf6a1b570236757ae8a7e92ca` passed CI
  [34419791745](https://github.com/wmichelin/clubdue/actions/runs/34419791745) and
  staging [34419791739](https://github.com/wmichelin/clubdue/actions/runs/34419791739).
  Running revision/image readback and manual staging smoke passed:
  `ghcr.io/wmichelin/clubdue@sha256:c371abda1eaee074a628dc20a85a6f7c9536d155784773fcc4f38499aaf9a39f`.
  Browser login/owner-preserving demo entry/customer book passed at 390/1440px
  with no page errors. PostgreSQL remains the active application database.

### Native property records — implementation

- Migration 006 and explicit native statements add site details, decimal
  measurements and dated notes. One composite property cascade preserves tenant
  boundaries. Native latest-100 history uses canonical UUID text for ties,
  preserving PostgreSQL's selection despite different native UUID ordering.
- Architect/staff: serialize initial site-detail saves and note submission keys
  with transaction/range locks; compare note retries exactly in Go. Preserve
  original authorship, date and timestamp; differing retries conflict. Keep
  decimal strings through decimal(14,3) storage. Historical PG-only dates and
  numeric NaN require migration preflight even though app validation rejects them.
- Unit-testing/QA: existing property-history and note-retry tests run on both
  engines; native tests cover first-save/update races, revision overflow rollback,
  changed retry body/author/date/property, cross-shop keys, decimal extremes and
  101 notes with tied timestamps. HTTP tests distinguish missing records from
  unavailable reads and reject failed writes without redirects or leaked errors.
- Product: property-read outages return 503 instead of missing-record feedback;
  failed note/details/measurement saves return 503, with 422 for invalid input and
  409 for conflicts. No new product page or external integration is introduced.
- DevOps: SQL runtime selection remains disabled; no live data is transferred.
  Full Go suite with both engines, vet, static build and isolated SQL rehearsal
  passed. Staging verification is the remaining check for this slice.
- Staging: https://clubdue-staging.michelindigitalmedia.com (verified `9246dea`;
  property-record changes pending). Production is unchanged.


- Property-record source `4ac47f801dea99c8b4fc41566fa308c091c45a29` passed CI
  [34420502263](https://github.com/wmichelin/clubdue/actions/runs/34420502263) and
  staging [34420502319](https://github.com/wmichelin/clubdue/actions/runs/34420502319).
  Running revision/image readback and manual smoke passed:
  `ghcr.io/wmichelin/clubdue@sha256:7050de3263cfb1c6887427c061390c0b9c20a957079649b69b83d59a66f87928`.
  Browser property/details/note/decimal-history/Maps checks passed at 390/1440px,
  with no browser errors or document overflow. The initial QA assertion checked
  a note inside collapsed history; opening it fixed the assertion, with no app
  change. Mobile screenshot reviewed. Native note-insert rollback/retry and HTTP
  validation/conflict review additions also passed before staging.

### Native photo storage records — implementation

- Migrations 007/008 and explicit native statements preserve private photos,
  pending uploads, quota accounting and inline recovery bytes. Version inventory
  survives property/customer/shop deletion; set-based retirement covers every
  deleted row, including cascades, and retains its earliest retirement timestamp.
- Architect/staff: shop-before-photo quota locks; photo-before-inventory retirement;
  commit key/inventory before Blob IO, then independently commit the returned
  version before publishing the photo. Trigger-bearing returns use OUTPUT INTO.
  Exact version comparison includes binary collation and byte length.
- Product/QA: pending uploads remain hidden/count toward quota; failed backfills
  retain existing visibility; foreign access never reaches Blob; cloud read failures
  stay failures. Gallery outages return 503, missing/malformed photo IDs 404.
  Existing photo retention and deletion limits are unchanged; no purge is added.
- Unit-testing: shared real-engine tests cover duplicate/concurrent uploads, both
  quotas, private handlers, failed storage, recovery reads and late deletion.
  Native tests cover multirow property/customer/shop cascade rollback, retained
  versions/timestamps, exact version conflicts and release of all four connections
  during Blob calls. Inventory-insert failure rolls back rows/quota before Blob;
  publication failure retains the version/pending photo and retries without a
  duplicate. Bounded SyncPhotoObjects resumes one pending row and then drains.
- DevOps: full Go suite with both engines, vet and static build passed. Focused
  phase-failure and handler regressions and final isolated SQL rehearsal passed. Runtime SQL selection and sync command wiring remain disabled/pending
  until the full port and transfer/restore gates pass.
- Staging: https://clubdue-staging.michelindigitalmedia.com (verified `4ac47f8`;
  native photo slice pending). Production remains unchanged.


- Photo source `1c7c8b754854e68b3c70009857f086d3486ba7d9` passed all CI jobs
  [34421134446](https://github.com/wmichelin/clubdue/actions/runs/34421134446) and
  staging [34421134661](https://github.com/wmichelin/clubdue/actions/runs/34421134661).
  Running revision/image readback and manual smoke passed:
  `ghcr.io/wmichelin/clubdue@sha256:42be3b8979dc054f6b85e6f944762de0b2183100478955cf25ad97025664d3b1`.
  Staging browser checks passed large upload through proxy, unsupported format
  feedback, private Azure image retrieval, foreign/anonymous denial, deletion and
  re-upload at 390/1440px without errors. Production is unchanged.

### Native asset service history — implementation

- Migrations 009–012 preserve the shared vehicle/equipment history table and
  submission keys. SQL Server prohibits its multiple cascading asset paths;
  one shop cascade plus set-based ownership, parent-update and deletion triggers
  preserve the current relationship contract. This is an engine-specific
  constraint implementation, not a new history or reassignment feature.
- Architect/staff: note writes lock shop (shared), asset, then submission key.
  Trigger parent/notes checks use current locking reads under RCSI. Orphaning
  identity/shop changes fail; same-shop asset moves preserve notes and target
  navigation. Ordinary asset edits skip unnecessary history locks. Direct SQL
  writes may deadlock with deletion; failure must roll back rather than replay.
- Product/QA: retain date-only NULL instants, explicit DST fold choices, original
  authorship and exact retry semantics. Invalid resource IDs return 404; invalid
  note kind/text and UTC instants outside years 0001–9999 fail before persistence.
  No new asset-move UI is included. Canonical UUID ordering retains latest history.
- Unit-testing: shared existing history/DST/privacy tests pass on both engines.
  Native tests cover multirow tenant rejection, referenced identity changes,
  same-shop moves, two-tenant direct/property/customer/shop cascade rollback, and
  all eight note-first/parent-first races against deletion/identity changes.
  Failed inserts release the key and permit retry; 102 retained entries verify
  event-date priority, tied recording timestamps and the 100-row UUID window.
- DevOps: PostgreSQL migrations are unchanged and SQL runtime stays disabled.
  Full both-engine suite, vet, static build and isolated SQL rehearsal passed. Bulk transfer/restore must fire these validation triggers, verify
  they remain enabled, and audit orphan/tenant relationships explicitly; SQL
  constraint checks alone cannot prove trigger-backed relationships.
  [Trigger ordering](https://learn.microsoft.com/en-us/sql/t-sql/statements/create-trigger-transact-sql?view=sql-server-ver17)
  and [bulk trigger behavior](https://learn.microsoft.com/en-us/sql/t-sql/statements/bulk-insert-transact-sql?view=sql-server-ver17).
- Staging: https://clubdue-staging.michelindigitalmedia.com (verified `1c7c8b7`;
  this asset-history slice pending). Production is unchanged.


- Asset-history source `71042dfa03f567fffa4f83e9597e1e44ae0a969b` passed all CI jobs
  [34422132769](https://github.com/wmichelin/clubdue/actions/runs/34422132769) and
  staging [34422132733](https://github.com/wmichelin/clubdue/actions/runs/34422132733).
  Independent running source/image readback and manual smoke passed:
  `ghcr.io/wmichelin/clubdue@sha256:8eb0f41829ce6a938015ed23ea6aedb5cec32723b3b86619b7cde49a88a61db6`.
  Browser asset-history checks passed separate vehicle/equipment records, date-only
  and timed notes, daylight-saving fold/gap handling, corrections, no-JavaScript
  fallback and delayed defaults at 390/1440px with no browser errors.

### Native standalone jobs — implementation

- Migration 013 and explicit native statements preserve standalone jobs and their
  change snapshots. Jobs cascade from shops, restrict referenced customer/property
  deletion and cascade their events. One job change and its event share a transaction.
- Architect/staff: shared parent locks precede submission-key/job locks. A retry
  with the original fingerprint still returns the original job after its property
  was detached and deleted. Exact Go fingerprints remain unchanged. Native JSON
  snapshots preserve optional empty fields and escaped Unicode; canonical UUID
  ordering and explicit NULLS LAST preserve list windows.
- Product/QA: existing opt-in jobs, surveys, fixed customer identity, optional
  planned dates, stale-save feedback and open/closed transitions remain unchanged.
  Customer-detail and new-job lookup failures now return 503 rather than a false
  missing-record response; missing/malformed IDs remain 404. Invalid UTF-8/NUL
  input is rejected before fingerprinting or persistence.
- Unit-testing: shared behavior passes on both engines. Native tests cover failed
  event writes rolling back create/edit/completion, competing revision updates,
  revision overflow, detached-property retries, years 0001/9999, escaped snapshots
  over 4,000 UTF-16 units, and 101-row list/history windows. Referenced customer and
  property deletion rolls back dependent history/photos and inventory retirement;
  whole-shop deletion removes jobs/events and retires photo inventory.
- DevOps: full both-engine Go suite, vet, static build and isolated native SQL
  rehearsal passed. Native SQL runtime
  remains disabled and PostgreSQL migrations are unchanged. Import must create
  customer/property parents, then jobs, then events: SQL Server cannot defer those
  foreign keys like PostgreSQL. No live database data has been transferred.
- Staging: https://clubdue-staging.michelindigitalmedia.com (verified `71042df`;
  this jobs slice pending deployment). Production remains unchanged.


### Native business setup — review and plan

- Product: preserve opt-in, read-only previews, canonical selected trade ordering,
  existing custom/disabled services and no automatic plans/jobs/prices.
- Architect/staff: store ordered trades as JSON strings, validating exact primary
  membership with OPENJSON; use OUTPUT INTO for triggered settings writes. Keep
  unbounded historical text, hash-indexed preset keys with exact comparisons and
  collision failure, and service ordering by exact name/canonical UUID in Go.
- Unit-testing/QA: share existing behavior across engines; verify concurrent first
  saves/service edits, duplicate selection, rollback after a later preset fails,
  Unicode and exact membership. Distinguish 422 invalid input, 409 stale state and
  503 unconfirmed saves; verify real owner demo browser behavior.
- DevOps: lock the shop before settings/preset writes; retain independent shop
  cascades, with no services-to-settings relationship that would delete custom
  services when trades change. Keep SQL runtime disabled. Transfer must audit
  PG array dimensions/lower bounds/NULL members, preserve unknown string IDs and
  duplicates, and verify target OPENJSON compatibility and trigger enablement.
  Confirm source text ordering rather than inferring it from a locale name.
- Staging: https://clubdue-staging.michelindigitalmedia.com (jobs slice deploying;
  business setup port not yet implemented). Production remains unchanged.


- Jobs source `35e076041999422930212b57d511f22e9b760326` passed all CI jobs
  [34423284223](https://github.com/wmichelin/clubdue/actions/runs/34423284223) and
  staging [34423284258](https://github.com/wmichelin/clubdue/actions/runs/34423284258).
  Running revision/image readback and manual staging smoke passed:
  `ghcr.io/wmichelin/clubdue@sha256:9724b380eafe966123ac45e9f00e7fb9de63b1fcb24b2bd717c897244ec2aed4`.
  Browser jobs checks passed creation, optional date, stale edits, completion,
  reopening, separate jobs, history and filters at 390/1440px without page errors.
  Production remains unchanged; this source is not approved for promotion.

### Native business setup — implementation

- Migrations 014/015 add ordered JSON trades with set-based exact primary membership
  and independently retained services. MAX text preserves historical values; preset
  identity uses a hash plus full binary/length comparison. Native reads sort services
  by exact name then canonical UUID, preserving the tested source ordering.
- Reviewed shared behavior and native failure tests pass: opt-in/read-only preview,
  preserved edits, duplicate keys, competing first saves and service edits, later
  preset failure rolling back settings and earlier inserts, overflow, independent
  settings deletion, whole-shop cleanup, long Unicode identities/descriptions,
  multirow JSON rejection and text/UUID ordering. Initial test compilation referenced
  an absent comparison library; using existing standard-library slice comparisons
  fixed the test harness without a new dependency.
- Business forms now distinguish 422 validation, 409 stale state and 503 unconfirmed
  saves. A connection lost after commit cannot prove failure; the message asks the
  owner to reload and check before retrying. Failed forms do not redirect or expose
  database errors. Both engines pass the HTTP behavior tests.
- Staging: https://clubdue-staging.michelindigitalmedia.com (verified `35e0760`;
  business setup implementation pending). SQL runtime remains disabled.

- Final checks: full Go suite with both real engines, vet, static build and isolated
  native SQL rehearsal passed. Architect/staff and product/unit/QA actual-code
  reviews found no blockers. Graph syntax/catalog remains 36 nodes and 64 edges.
  Actual source ordering and PG array preflight remain required before SQL cutover.

### Native templates and agreements — review and plan

- Product: preserve template CRUD, signed stored prices/counters, optional property
  and template links, captured agreement terms and current last-write-wins edits.
  This adds no billing or payment features. Template changes cannot rewrite prior
  agreement snapshots; missing creation acknowledgement requires checking the list
  before retrying, because agreement creation has no submission key.
- Architect/staff: one shop cascade per table, composite customer restriction and
  trigger-backed nullable property/template relationships. Retain parent locks in
  shop/customer/property/template order before agreement writes; snapshot all terms
  in one statement. Nulling one reference must not lock the unchanged other parent.
- Unit-testing/QA: share tenant/snapshot tests; cover deleted parent pointers,
  snapshot survival, concurrent parent deletion and snapshot capture, multirow
  rollback, historical text/JSON arrays, cents/counters and date boundaries.
  Failed reads must not masquerade as 404; failed writes cannot redirect as saved.
  Reject malformed/overflowing numeric form input before it becomes a zero value.
- DevOps: keep SQL runtime disabled. Stronger parent-update guards close a raw-PG
  gap and affect import: property customer moves precede agreement repair today.
  The native importer must handle authorized reference changes atomically and be
  tested before activation; do not silently detach unrelated jobs. Parent-first
  import, trigger enablement/orphan audits, JSON array preflight and text ordering
  remain mandatory. Agreement forms still depend on unported recurring schedules.
- Staging: https://clubdue-staging.michelindigitalmedia.com (business slice deploying;
  templates/agreements not yet implemented). Production remains unchanged.


- Business setup source `0b38de54bb5f71ba7b28ff43b79285d99e432da0` passed all CI jobs
  [34424095589](https://github.com/wmichelin/clubdue/actions/runs/34424095589) and
  staging [34424095467](https://github.com/wmichelin/clubdue/actions/runs/34424095467).
  Running revision/image readback and manual staging smoke passed:
  `ghcr.io/wmichelin/clubdue@sha256:54c6cf0b4e4f5c4bcb5c19f75d6d5e5d258dedd97e3eaa6cd9d5806a68d8eb43`.
  Browser mixed trades, preview, customization/disable, reapply, General trade
  change and stale second-tab rejection passed at 390/1440px without errors.
  The first browser run used a single-page context; opening an explicit browser
  context repaired the second-tab test, with no app change. Production is unchanged.

### Native templates and agreements — implementation

- Migrations 016–019 preserve template and agreement identity, signed stored cents
  and counters, optional links and all captured terms. Ordered window arrays use
  validated JSON without truncating historical strings. Template names/external
  IDs remain exact, unbounded hash-indexed identities. Parent delete cleanup leaves
  snapshot fields and agreement timestamps unchanged.
- Architect/staff review found two response mismatches; both fixed and tested:
  deleted-plan stale updates retain their NULL-safe comparison/conflict outcome,
  and recurring-service creation no longer masks agreement read outages as 404.
  Ordinary agreement validation retains the submitted form. Explicit trigger column
  gates skip parent lookups on status/counter-only writes and unrelated link cleanup.
- Native tests pass shared tenant/snapshot behavior, monthly/nonstandard snapshot
  capture, template edit/delete survival, independent prices/counters, editable
  same-shop customer/property, protected customer/property deletion, fully linked
  shop cascade, direct parent guards, multirow invalid insert rollback, long Unicode
  names/identities/JSON, years 0001/9999 and signed integer boundaries. Concurrent
  property/template deletion succeeds with both parent locks held first; agreement
  creation waits for a complete locked template edit and captures its entire result.
- Product/unit/QA: numeric form parsing rejects malformed/overflowing values before
  they become zero; optional new-template blanks retain existing defaults. Plan
  prices/service counts are nonnegative and terms positive in forms; historical
  stored schema breadth remains. Failed saves do not redirect or leak dependencies.
  Unconfirmed creates tell users to check the list before retrying. Both engines
  pass focused form availability and invalid-number tests.
- Transfer must insert original snapshot columns directly, including rows with
  already-null optional links; CreateAgreement would recapture current template
  terms and must not be used for transfer. Stronger parent guards require the
  reviewed import sequence before SQL activation. Recurring schedules, dashboard,
  renewal/visit processing and runtime selection remain unfinished.
- Staging: https://clubdue-staging.michelindigitalmedia.com (verified `0b38de5`;
  template/agreement changes pending). Full regression and isolated rehearsal running.

- Final checks passed: full Go suite with both real engines, vet, static build and
  isolated native SQL rehearsal. Product/QA's two final HTTP corrections also pass
  focused checks on both engines: missing customer/invalid date return 422 with
  notes retained, and plan-edit 422 responses declare HTML before sending headers.
  All six review perspectives are recorded above; graph renders 36 nodes/64 edges.
  CI and staging verification are the remaining gates for this slice.
