# Sideyard: six verticals and configurable service workflows

For the clearer, release-by-release execution sequence and six-role delivery
review, see [Sideyard execution plan](SIDEYARD-EXECUTION-PLAN.md). That document
governs delivery order; this one retains the detailed requirements and research.

Planning date: September 8, 2026. Status: proposed; no application changes or
deployments authorized by this planning request. Baseline: `50b5c85`, clean
working tree before this document. This plan expands the earlier commitment-book
scope in `PRODUCT-MARKET-FIT-PATH.md`; its exclusions of invoicing and chemical
records are historical decisions to revisit through these scoped releases.

## Product decision

Build one Sideyard workspace with six editable industry presets. Each preset
supplies useful service templates, fields, checklists, pricing options, and
terminology. Owners can select multiple verticals and enable individual
capabilities. Selecting Landscaping must not prevent adding House washing.

Common workflow: customer → service location and/or asset → quote → accepted
work → scheduled visit → field record → billing and payment → repeat business.
Recurring service is one supported work type; a one-time job must not require
an artificial annual agreement.

Keep four concepts independent: what was sold, when work happens, when payment
is requested, and when money is received. Changing one must not silently rewrite
the others. Support mobile entry with a short default form and optional detail.

US owner-operated businesses and small crews are the initial planning
assumption. The vertical definitions below are proposed product requirements,
not evidence that interviews have validated every trade. Start operational
validation with the landscaping friend; preserve the existing HVAC workflow.

## Clearly defined verticals

| Preset / stable ID | Initial business scope and workflow | Important records and defaults | Acceptance example |
|---|---|---|---|
| Auto detailing / `auto_detailing` | Mobile or shop-based interior/exterior detailing, recurring maintenance, coating services | Customer-owned vehicles; year/make/model, size class, condition, optional plate/VIN, service address distinct from vehicle; package/add-on pricing; pre-existing damage photos; inspection and handoff checklist | One customer books two vehicles at different locations; each keeps its own history, quote, photos, and next appointment. A shop job needs no customer property. |
| House washing / `house_washing` | House, roof, driveway, patio and other exterior cleaning | Surface type/material; stories; separate wall, roof, driveway and walkway measurements; water/access details; before/after photos; optional chemical use; weather sensitivity; minimum job price | Quote house washing plus a driveway from separate measurements, take a deposit, move the visit for rain, and collect the balance without duplicating the deposit. |
| Landscaping & lawn care / `landscaping` | Recurring mowing/maintenance, seasonal cleanup, lawn treatment programs, small quoted landscape work | Turf and bed areas, zones/exclusions, access and irrigation notes; weekly/biweekly work; named seasonal treatment rounds; chemical/application records; seasonal package billing | Sell one contract covering four or five treatments, paid upfront or billed after each completed treatment; document applications and delay completion-triggered billing when work moves. |
| HVAC / `hvac` | Maintenance memberships, inspections, diagnostics, repair, bounded replacement jobs | Equipment type/make/model/serial, install and warranty dates, filters, service readings and checklist; spring/fall visits; diagnostic/repair quotes; material usage | Two units at one property retain separate histories; the existing seasonal plan still generates and counts visits correctly while a repair is billed separately. |
| Plumbing / `plumbing` | Residential/light-commercial service calls, repairs, maintenance and bounded installations | Fixtures, water heaters, shutoff/access locations, serial/warranty details where relevant; diagnosis, labor/parts, inspection/photos; callout minimum and deposit/balance options | Record a leaking-water-heater visit, approve a replacement quote, collect a deposit and balance, and retain the installation and warranty history. |
| Electrical / `electrical` | Residential/light-commercial diagnosis, repair, maintenance and bounded installations | Panels/circuits, equipment and identifiers, access, diagnostic notes/readings, inspection/checklists, parts/labor, permit/inspection references where applicable | Diagnose an issue, quote an installation, record circuit labels and completion photos, then track an inspection follow-up separately from payment. |

Large construction projects, financing/underwriting, payroll, full accounting,
engineered electrical/HVAC calculations, and advanced fleet dispatch are later
product decisions. Basic project references and milestones can fit quoted work
without claiming these broader capabilities.

## Baseline and evidence

Inspected repository instructions, engineering philosophy, README, product and
scheduling docs, schema migrations, routes, handlers, templates, domain code,
and CI. Used `git status --short`, `git log`, `rg`, and targeted file reads.
No tests were run because this delivery changes only the planning document.
No root-file moves or structural cleanup are proposed. Keep the Go binary,
embedded templates, Postgres, existing domain packages and deployment workflow.

| Finding | Observed evidence | Proposed change / impact |
|---|---|---|
| F1: Basic customer/site/asset records exist | `internal/app/store.go`: `Customer`, `Property`, `Equipment`; `customer_handlers.go`: `handlePropertyCreate`, `handleEquipmentCreate`; migration `001_init.sql` | Extend site records with measurements and access details. Existing equipment is property-bound; add customer-owned vehicle assets without forcing vehicles to remain at one address. High confidence; foundation for all presets. |
| F2: Notes exist, but not a complete field history | Agreement `Notes`; `internal/servicework/servicework.go`: `UpdateOccurrenceStatuses`; seasonal `visit_dues.notes` | Add authored, timestamped notes and media attached to a site, asset or visit. Current outcome notes cannot serve as the only draft/site-visit journal. High confidence. |
| F3: Work is agreement-oriented | `service_schedules.agreement_id` in migration `014`; `CreateSchedule`, `GenerateScheduleOccurrences`; migration `018` date overrides | Add one-time jobs and finite named treatment rounds while retaining existing recurrence identities, outcomes and HVAC seasonal paths. High impact; compatibility design first. |
| F4: Price metadata is not a billing system | Monthly/annual checks in migrations `001` and `008`; `handleHomeownerRenewalPayment` is a staging stub; README payment boundary | Add quotes, payment schedules, invoices and payment reconciliation as distinct records. Never interpret agreement `price_cents` or renewal `past_due` as an unpaid invoice balance. High confidence; highest financial correctness risk. |
| F5: Private media needs explicit boundaries | `security.go`: `setSecurityHeaders`, `requestBodyLimit`; `handlers.go`: demo mutation/upload allowlist; `auth.go`: role data | Design private attachment access and actual role enforcement. A role column alone does not establish crew permissions. Preserve demo restrictions. High confidence. |
| F6: External capabilities are absent | `go.mod`, route inventory, README; `servicework.go`: `ExportRows` exports notes to CSV | Add small injected provider interfaces for measurements, weather and payments. Keep navigation as map links initially. Audit exports so internal notes do not become customer or routing payloads. High confidence. |

F1–F4 follow the philosophy's smallest-coherent-change and compatibility rules;
F5 prioritizes correct, secure behavior; F6 preserves boundaries and testable
external dependencies. Typed business records remain authoritative. A custom
field must never be the source of truth for balances, area units or timestamps.

## Configuration model

Onboarding asks for primary trade, additional services, mobile/shop work style,
and preferred billing options. Apply a versioned preset once, then allow edits.
Preset updates show a preview and never overwrite a shop's customized templates
or accepted customer terms. Existing tenants keep their current configuration
until they opt in. A General services choice remains available.

| Configuration | Owner can change | Guardrail |
|---|---|---|
| Service catalog | Names, packages, optional extras, duration, fixed/hourly/unit/tier pricing, minimum charge | Snapshot quote inputs and accepted prices; later edits affect new work only. |
| Work pattern | One-time, repeating, seasonal, finite named rounds; service windows; checklist | Each round has its own identity/order/window. Billing policy explicitly determines whether completion triggers an invoice. |
| Property/asset forms | Relevant built-in fields, optional/required flags, display units; a bounded set of text/number/date/select/checkbox extras | Scope fields to shop and record type; hide/archive rather than destroy values. No user scripting or arbitrary workflow engine. |
| Field records | Required completion checklist, photos, application fields, staff assignments | Internal by default; customer-visible summary is a separate explicit action. |
| Billing | Upfront, after each visit/treatment, one-time completion, recurring calendar, grouped actual work, deposit/balance, milestones and fixed installments; optional prepay discount | Customer-level override is recorded in agreed terms; collection and refund rules are explicit. |
| Navigation and weather | Google/Apple Maps preference; relevant weather fields and reminders | Site coordinates can be manually confirmed; no background location tracking. |

For a landscaper who also washes houses: enable both presets, share one customer
and property, price turf work from turf area and washing from the selected
surface, and keep each service's schedule and billing terms independent.

## Buddy feedback translated into requirements

### Property details, site-visit notes and internal photos

Provide a property page with overview, access details, measurements, assets,
visit history and internal notes/photos. A pre-quote site survey is a valid visit
even when there is no paid job yet. Notes can be entered before, during and after
service; record author, actual visit time, entry time and revision history.
Track actual work time separately from today's completion-recording timestamp.

Photos support before/after, damage, access and general categories with captions
and links to the relevant visit/asset. Start with private object storage,
tenant-checked downloads, bounded image sizes/counts, validated image decoding,
and metadata stripping. Plan HEIC handling or a clear mobile fallback. Test
interrupted uploads, retries and a visible saved/unsaved state. Full offline sync
is a later feature, so do not imply offline saves succeeded.

Owner access comes first; crew access requires explicit assignment/permission
checks. Private notes, gate details and raw photos must not appear in customer
quotes, receipts, renewal pages or public links. Separate internal export from
customer export; existing work CSV includes notes and needs deliberate review.
Retention/deletion must cover both database metadata and stored objects.

### Lawn, landscape, house and driveway measurements

Store multiple named areas per site: lawn/turf, beds, driveway, walkway, patio,
roof, building footprint, floor area, exterior wall area and custom zones.
Support linear measurements separately for perimeter or edging. Each value
records type, unit, source, measured date, author and estimate/verified status.
Unknown is distinct from zero. Keep old measurements when values are revised.

Do not substitute floor area or building footprint for exterior wall area in a
washing quote. Track exclusions and avoid double-counting overlapping zones.
Store the measured value used on a quote so later corrections cannot alter
accepted pricing. Start with manual entry and a reviewed CSV mapping.

Deep Lawn advertises area measurements, bulk data and an API add-on. Its current
API page lists an additional $85/month and access by request. Recommendation:
support imported/manual values first, then a customer-authorized connector after
confirming contract, actual API schema, coverage, quotas and data retention rights.
Do not build satellite segmentation or assume a self-service API is already
available. [Deep Lawn API](https://deeplawn.com/bulk-data-&-api),
[measurements](https://deeplawn.com/measurements).

### Chemical tracking

Separate product use at a visit from inventory management. First release:
product catalog, label/SDS reference and version, registration identifier where
applicable, application location/area, actual start/end time and timezone,
applicator, product quantity and unit, carrier/mix quantity, recorded dilution or
rate, method, purpose, notes and linked weather snapshot. Support multiple
products in one application without silently treating concentrate and diluted
volume as the same quantity. Retain submitted records; corrections add history.

Lawn treatments, washing chemicals and detailing products share usage primitives
but have different required fields. HVAC refrigerants need a dedicated extension
if requested; do not equate a simple material log with refrigerant reporting.
Inventory receipts, stock deductions, truck stock, lot tracking and replenishment
are a subsequent slice after usage records work reliably.

Treat the initial feature as an operational application log. Validate the pilot's
products and state-specific record/export requirements before claiming compliance.
EPA explains that pesticide label directions are enforceable; the app should
retain relevant references rather than invent universal rates or a “safe to spray”
decision. [EPA label guidance](https://www.epa.gov/pesticide-labels/introduction-pesticide-labels).

### Weather at the time of application

Capture a fixed weather snapshot keyed to the property's confirmed coordinates
and actual application time: temperature, wind speed/direction/gusts, humidity,
precipitation and source. Preserve observation/forecast time, retrieval time,
station or model identity, distance where available and units. Show technician
observations separately; a model forecast is not an on-site measurement.

For a US pilot, evaluate NWS first: its API exposes forecasts and observations,
with no usage fee and published rate-limit caveats. Observations may lag about
20 minutes, so allow manual conditions and a clearly labeled pending/unavailable
snapshot. Saving field work must survive provider downtime. Keep an archived
snapshot instead of re-fetching “current weather” when a record is opened later.
[NWS API](https://www.weather.gov/documentation/services-web-api).

Open-Meteo is a candidate for modeled weather and historical lookup; production
use needs its commercial service plan, and historical APIs currently require
Professional or higher. Its model archives must remain labeled as modeled data.
[Commercial plans](https://open-meteo.com/en/pricing),
[historical forecasts](https://open-meteo.com/en/docs/historical-forecast-api).

Later weather-assisted rescheduling can suggest affected visits and let the
owner review a bulk move. It must not silently move work, change a payment date,
or approve a chemical application based solely on a forecast.

### Google or Apple Maps

First slice: a Navigate button on a job/property and a remembered map preference,
plus a manually ordered daily stop list. Send only the destination needed for
navigation. Google supports cross-platform Maps URLs without an API key; Apple
documents map links and newer unified URLs. Verify iOS, Android and desktop
fallback behavior. [Google Maps URLs](https://developers.google.com/maps/documentation/urls/get-started),
[Apple Maps URLs](https://developer.apple.com/documentation/mapkit/unified-map-urls),
[legacy Apple links](https://developer.apple.com/library/archive/featuredarticles/iPhoneURLScheme_Reference/MapLinks/MapLinks.html).

Multi-stop export and automated route optimization are separate later releases;
respect each provider's URL/waypoint constraints. Navigation links alone must
not be marketed as optimized scheduling or dispatch.

### One contract, four or five treatments

Confirmed by the user: one contract covers four or five separate treatments.
The customer chooses payment upfront or billing after each completed treatment.
These are two collection options for the same contracted program. This example
must not limit billing across the app. The shared billing capability also covers
one-time completion, recurring calendar charges, periodic invoices for actual
work, deposits/balances, milestones and fixed installments. Every trade can select
and override these terms; the execution plan defines their acceptance checks.

| Work sold (illustrative prices) | Customer payment choice | Service and billing behavior |
|---|---|---|
| One $1,000 contract; five named treatments | $1,000 upfront | Five treatments remain open; their completion never bills the customer again. |
| Same $1,000 contract; five named treatments | $200 after each completed treatment | Each billable completion creates one linked invoice; all invoices and payments roll up to the same contract. |
| One $1,000 contract; four named treatments | $1,000 upfront | Four treatments remain open after payment. |
| Same $1,000 contract; four named treatments | $250 after each completed treatment | Four completion-triggered charges, each recorded against the original contract. |

Store the agreed total, included treatments and the amount allocated to each
treatment. Equal amounts are a convenience; allow unequal treatment prices whose
sum matches the agreed contract total. Preserve accepted terms and any optional
prepay discount. Show contract total, billed to date, paid to date, outstanding
invoices and remaining unbilled contract value separately.

For after-treatment billing, completing a treatment creates its charge exactly
once. Scheduling or rescheduling does not bill anything. A weather delay defers
the charge until actual completion. Recording completion is not evidence of
payment: cash/check receipts or verified provider events settle the invoice.
Link repeat webhook events and repeated completion submissions to the existing
charge instead of issuing another invoice.

A skipped treatment, callback, redo or cancelled program needs an explicit
policy; it must not silently bill, refund, or rewrite the contract. An approved
credit/change records its reason and preserves history. Early payoff clears
existing invoices and prepays remaining treatment allocations so later completion
cannot charge them again. Support partial payments and external payment
references. Specify currency, integer minor-unit rounding and tax/discount totals.
Actual collections remain separate from projected agreement revenue and renewal
status. Verify that the full contract total is not invoiced in addition to its
individual treatment charges.

Recommend Sideyard-owned contract billing terms with invoice/collection records
linked to treatments. Use hosted payment pages and verified webhooks; never mark
paid from a redirect or a button click. Stripe Connect is a candidate for each
contractor receiving payment in their own connected account. Keep the contractor's
customer payments separate from Sideyard subscription billing. Confirm account
configuration, fees and onboarding before live activation.
[Stripe direct charges](https://docs.stripe.com/connect/direct-charges),
[Connect webhooks](https://docs.stripe.com/connect/webhooks).

Autopay after completion is an optional collection method requiring explicit
customer authorization and separate testing. The pilot does not depend on
Stripe's native calendar installment feature. Confirm whether the buddy sends
an invoice after treatment or automatically collects an authorized payment;
either method uses the same completion-triggered contract charge.

## Architecture and compatibility

Keep shop/customer/property/agreement identities. Add typed records for site
measurements, customer assets, authored notes, attachments, jobs, job-to-asset
links, service templates, checklist versions, quotes/lines, contract billing terms,
billing schedules and charge allocations, invoices/payments, applications and weather snapshots as each
capability ships. Do not create all tables up front. Every owned relation carries
`shop_id` and tenant-safe references, including blob metadata and provider IDs.

New job records can represent standalone work or reference exactly one existing
recurring occurrence/seasonal visit using tenant-safe, unique foreign keys.
For linked work the original occurrence remains the source of scheduled date and
completion; do not introduce a second mutable status/date ledger. Standalone
jobs own their scheduling state. Bridge existing read/write paths deliberately
and test that a completion still counts once. Historical bulk conversion is
unnecessary for the first slice; existing history remains readable.

Treat finite program rounds as explicitly named, ordered work with service
windows. Keep migration `018`'s original recurrence identity and date override
semantics for repeating services. New actual performed timestamps must retain
the historical recorded-at values instead of guessing old application times.

Presets are versioned defaults; core facts use typed columns/tables. Bounded
optional field definitions are a later convenience, not a replacement for the
domain model. New packages should follow demonstrated ownership, for example
`internal/properties`, `internal/fieldrecords`, `internal/quoting` and
`internal/billing`; do not reorganize unrelated existing code to make these fit.

Inject storage, measurement, weather and payment clients at construction. Use
transactional outboxes and idempotent provider requests for external writes.
Webhook deduplication keys include provider/account/event; validate signature,
tenant ownership, environment, amount and currency. Handle delayed/out-of-order
success, failure, refund and dispute events with reconciliation. Retries must
not create duplicate charges or allocate one payment twice.

The public demo uses synthetic preset examples and mocked integrations; it cannot
charge money, send customer messages, incur measurement fees or upload arbitrary
media. Retain the existing signup-alert pipeline and both production hostnames.

## Delivery review

| Perspective | Concrete recommendation |
|---|---|
| Product | Six presets, one workspace; validate landscaping first and make each trade earn its support claim through its acceptance example. |
| Architecture | Separate sold scope, visits, billing and settlement; extend existing identities additively and avoid duplicate occurrence state. |
| Unit testing | Test mixed presets, units, visibility, finite rounds, billing rounding and retry behavior through injected provider fakes. |
| Engineering | Add the smallest typed capability per release, preserve template snapshots and CSV compatibility, defer generic workflow machinery. |
| DevOps | Private media lifecycle, provider usage ceilings, isolated connected accounts, durable workers and environment kill switches are prerequisites to activation. |
| QA | Test on phones with failed uploads/provider outages, cross-tenant access attempts, historical records, and an actual end-to-end scenario for each vertical. |

## Ordered work items

| Item | Objective and affected code | Dependencies / checkpoint | Risks, validation and recovery |
|---|---|---|---|
| V0: Confirm examples | Use one redacted seasonal quote, treatment log and billing schedule from the friend; record the preferred payment tool and operating state. Confirm preset wording with trade operators. Update this plan; no outreach without authorization. | Billing interpretation confirmed: one contract, four or five treatments, upfront or after-treatment payment. | Check examples against the matrix; mark unsupported requirements explicitly. No data migration. |
| V1: Presets and property foundation | Add shop preset settings, site editing/details and typed measurements around `store.go`, `customer_handlers.go`, `customer_form.html`; add customer-owned assets for detailing. New additive migrations. | Freeze field/unit/identity contracts first. Checkpoint: configure mixed Landscaping + House washing and a two-vehicle detailer. | Preserve existing sites/equipment and CSV values; cross-tenant/invalid-unit/template-version tests. Disable new edit surfaces to recover, retain saved data. |
| V2: Field work and navigation | Add authored notes, private media, actual visit times, one-time/site-survey jobs, finite program rounds, minimal assignment/appointment windows and map links. Touch servicework/visits boundaries, new field-record handlers, security policy and demo allowlists narrowly. | V1. Ship notes/navigation, then media, then job bridges as separate small checkpoints. Verify existing HVAC and recurrence behavior after each bridge. | Test visibility, stale edits, mobile interruption, DST/timezones, paired job/occurrence identity and entitlement counts. Disable new creation if needed; retain a reader for saved new records. |
| V3: Quotes and billing ledger | Add service line items, fixed/unit/tier prices and minimums, quote acceptance snapshot, configurable upfront/completion/date/milestone billing, grouped actual-work invoices and manual payment allocation. New quoting/billing packages and templates; audit existing revenue metrics and renewal terminology. | V1; V2 for per-visit billing. Checkpoint: every shared billing pattern reconciles, including upfront and after-treatment four/five-treatment contracts. | Test uneven cents, partial/early payments, discounts/tax totals, cancellation/credit and duplicates. Never migrate existing agreements into invoices automatically. Freeze issuance on failure; retain financial records and correct by adjustment. |
| V4: Chemical records and weather | Add product catalog and application records, manual conditions, provider snapshot adapter and readable history/export. | V1–V2; can proceed separately from V3 once visit/time contracts are stable. Checkpoint: document a treatment with multiple products and unavailable weather. | Test units, actual-vs-recorded times, retries, stale/model data labels, immutable submitted history. Manual entry remains available if provider is disabled. Validate regulated exports before compliance claims. |
| V5: Live payment collection | Connect contractor accounts, hosted collection, signed webhooks, durable retries, receipts and reconciliation against V3. Wire separately from existing homeowner payment stub and signup alerts. | V3 and a tested account/fee model. Sandbox first; verify two contractors stay isolated. | Test duplicate/delayed events, pending ACH, failed collection, refunds/disputes and replay. Kill switch stops new collection while webhook/reconciliation processing continues. Do not roll back to a binary that loses settlement events. |
| V6: Measurement integration and vertical pilots | Implement reviewed import then Deep Lawn adapter if access/terms justify it; add six synthetic preset walkthroughs and vertical-specific demo/landing copy. | V1 measurements; API access is independent of basic adoption. Advertise only capabilities verified through V2–V5. | Contract tests, quota caps, import preview/idempotency and accepted-quote immutability. Disable provider and retain saved/manual values. Each vertical completes its acceptance example before its launch claim. |

Recommended first usable release: V1 plus the notes/navigation slice of V2.
Next close the friend's loop with program rounds, V3 billing and V4 application
history; activate payment collection through V5. Detailing, plumbing and
electrical become credible as one-time jobs, assets, appointments and quoting
are verified. Define all six now; release support claims according to evidence.

One implementation owner integrates the work. V1 is foundational; V2 precedes
application records, and V3 precedes live collection. Weather and measurement
adapter investigations can proceed independently after their contracts settle.
No separate agents are assigned by this plan.

## Validation and launch criteria

For each implementation slice, run focused behavioral tests followed by the
repository's required checks with a disposable Postgres database:

```sh
go vet ./...
CLUBDUE_REQUIRE_POSTGRES_INTEGRATION=1 go test -count=1 ./...
CGO_ENABLED=0 go build -o /tmp/clubdue ./cmd/clubdue
./scripts/production-rehearsal.sh
./scripts/pilot-onboarding-rehearsal.sh
./scripts/smoke-test.sh staging
```

`DATABASE_URL` must point to the disposable database for the Go integration
suite. Run relevant provider contract/sandbox tests and mobile browser acceptance
in addition; mocked success alone cannot establish payment or upload delivery.
Check additive migrations against old data and assess semantic rollback, not
just whether an old binary starts. Retain checksum validation and existing
tenant/demo isolation, imports, renewals, calendar and notification regressions.

Staging review URL: https://clubdue-staging.michelindigitalmedia.com.
No new staging revision is created by this planning document. Each future slice
must pass CI, deploy to staging and show its exact revision and results before
production approval under the release workflow.

Proposed pilot exit criteria: the friend completes one entire treatment-to-bill
workflow and reconciles prepaid and after-treatment billing for both four- and
five-treatment contracts; field notes
can be saved from a phone in under a minute in an observed trial; all test payment
balances reconcile; no internal-record exposure or duplicate work/charges;
operators demonstrate each vertical's acceptance example. Validate repeated use
over a four-week pilot before a broad all-trades launch. These are proposed
targets, not measured results.

## Approval gates and open questions

Planning is complete enough to sequence implementation; implementation itself
needs a new instruction because this request was for a plan. This is a scope
boundary, not a request to approve research already performed.

Confirmed in chat: four or five separate treatments belong to one contract, with
upfront payment or billing after each treatment. Resolve before dependent
activation: the buddy's current payment tool and preferred collection method; state and
product types for application records; Deep Lawn access and budget; crew access
and customer-sharing expectations. Defaults above allow independent foundation
work without answering all of these immediately.

Live payments, paid provider subscriptions, external customer communications and
claims of regulatory reporting require their concrete setup and evidence to be
reviewable before activation. Any historical backfill needs a dry-run and
reconciliation plan. Production releases follow `AGENTS.md`'s staging review
and explicit approval gate; the earlier notification deployment authorization
is not treated as blanket authorization for this new product expansion.
