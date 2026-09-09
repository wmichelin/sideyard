# Sideyard execution plan

For implementation progress after this planning handoff, see
[delivery status](SIDEYARD-DELIVERY-STATUS.md).

## Status update

Status: verified — execution-plan coverage and sequencing reviewed; software not implemented.
Last completed: reviewed the entire verticals proposal and the confirmed treatment billing rule through all six delivery perspectives.
Now: planning handoff, September 8, 2026.
Next: Release 1 when implementation is requested.
Evidence: source baseline `50b5c85`; [product requirements and research](SIDEYARD-VERTICALS-PLAN.md).
Staging: https://clubdue-staging.michelindigitalmedia.com (pending for this work; no new deployment).
Blocker: none for planning. Provider activation decisions are listed below.

Only documentation changes are in scope now. No implementation, database
migration, purchase, external message, commit, push or deployment is included.
This document governs execution order; the linked verticals plan supplies the
detailed product requirements and research. The six perspectives below were
reviewed by the planning agent; no separate specialist agents were run.

## Delivery brief

**Outcome:** a contractor can configure Sideyard for their trade, keep customer
and job details together, quote work, complete it and track payment.

**Six presets:** auto detailing, house washing, landscaping/lawn care, HVAC,
plumbing and electrical. A shop can combine presets and customize its services,
fields, checklists and pricing. All use the same workspace and shared records.

**Billing is configurable across every trade.** Owners choose when to bill and
how to collect payment at the service, quote or contract level. A preset suggests
defaults; it never limits a trade to one billing method.

**Confirmed landscaping example:** one contract contains four or five treatments,
paid upfront or billed after each completed treatment. This is a required example,
not the universal billing model. Moving treatment dates affects billing only when
the agreed trigger is treatment completion; completion itself never proves payment.

**Preserve:** current customers, agreements, recurring and seasonal work,
imports, calendar, renewals, private tenant boundaries, signup alerts, existing
Sideyard and ClubDue URLs. Existing shops are not forcibly reconfigured.

**Priority:** P1 product expansion. Seven reviewable releases, each divided into
small implementation slices. Finish each slice's checks before promoting it.
Provider setup is allowed to wait without blocking unrelated usable features.

## The seven releases

| Release | What you will be able to do | Demonstration that finishes the release |
|---|---|---|
| 1. Set up your business | Choose one or more of the six trades and edit the relevant defaults. | Configure a landscaper who also washes houses; a detailer sees vehicle-related defaults. |
| 2. Keep a complete customer record | Store property/vehicle/equipment details, measurements, internal notes and photos; open directions. | Open a property on a phone and find its access instructions, turf/driveway areas and previous photos. |
| 3. Plan and record work | Schedule one-time jobs, repeating service and four/five-treatment programs; assign work and complete checklists. | Move treatment three for rain, complete it later, and preserve every other treatment and its history. |
| 4. Quote and track what is owed | Choose upfront, completion, recurring, deposit/balance, milestone or installment billing; record payments received elsewhere. | Reconcile treatment contracts, a membership, an installation and a one-time repair using different billing terms. |
| 5. Document applications and weather | Record chemicals/materials used and conditions at the actual application time. | Review a treatment's products, quantities, treated area, applicator, photos and saved weather. |
| 6. Accept online payments | Let each contractor collect customer payments and reconcile payment outcomes. | Pay a test invoice, receive its receipt, and see the correct contractor's contract balance update once. |
| 7. Connect measurements and validate all trades | Import measurements, add Deep Lawn when access is ready, and verify six complete trade walkthroughs. | Reuse a measurement in a quote and complete the acceptance scenario for every preset. |

**First useful checkpoint:** Releases 1 and 2. **Friend's complete operating
workflow:** Releases 3–6 added to that foundation. Release 7 establishes broader
trade readiness and optional measurement automation. No calendar delivery date
is promised before implementation and provider-access checks establish effort.

## Execution tickets and acceptance checks

### Release 1 — Business setup

Depends on: none. Covers original V0 and the preset portion of V1.

- **E1.1 — Settings and preset versions.** Add a shop-scoped settings record,
  primary/additional trade selection and General services fallback. Preserve
  existing configuration. Define the six preset IDs from the source plan.
- **E1.2 — Editable defaults.** Seed relevant field selections and draft service
  templates once. Future releases activate their checklists/prices as available;
  do not show working controls for capabilities that have not shipped. Allow
  renaming and disabling defaults without deleting records that use them.
- **E1.3 — Safe updates.** Preview preset changes and apply only owner-selected
  changes. Existing customized templates and accepted terms remain intact.

Pass when: combining trades does not duplicate shared customers or services;
switching presets does not erase data; one tenant cannot edit another's settings.

Code boundary: shop settings, new preset definitions, settings handlers/templates,
`internal/app/store.go`, plan-template creation and additive migrations.
Recovery: disable preset application and preserve saved settings/templates.

### Release 2 — Customer, property and asset records

Depends on: E1 settings/field contracts. Covers the rest of V1 and early V2.

- **E2.1 — Property and asset editing.** Add property details and access notes;
  extend equipment fields; add customer-owned vehicles with a service location
  separate from vehicle identity. Support multiple sites/assets per customer.
- **E2.2 — Measurements.** Add named areas, linear measurements, units, source,
  measured date and revision history. Distinguish lawn, beds, driveway, footprint,
  floor area and washing surface. Unknown values stay distinct from zero.
- **E2.3 — Internal timeline.** Add authored notes to customers/sites/assets and
  existing visits. Store actual event time and entry time separately. Add Google
  and Apple navigation links from the service location.
- **E2.4 — Private photos.** Implement bounded uploads, captions/categories,
  private retrieval and deletion/retention. Validate formats and mobile handling,
  including HEIC or a clear fallback; display upload failure and allow safe retry.
  Ship owner access first; E3.3 supplies crew access. Never imply offline success.

Pass when: two vehicles keep separate histories at different locations; editing
one measurement preserves its history; private notes/photos cannot be fetched
by another tenant or exposed through public pages and customer exports. Test
interrupted uploads and unauthorized object URLs on phone-sized screens.

Code boundary: customer/property handlers and templates, `store.go`, new field
record/storage code, narrowly scoped security/upload policy changes. Preserve
the public demo's existing upload restrictions; use synthetic demo media.
Recovery: turn off new uploads, preserve authorized reading, and reconcile orphan
objects. Object storage, backup and deletion behavior must be proven before use.

### Release 3 — Scheduling and field work

Depends on: E2 identity, note and timestamp contracts. Covers remaining V2.

- **E3.1 — Jobs and site surveys.** Add standalone jobs and pre-quote site visits,
  plus links to existing recurring/seasonal visits. Keep a single authoritative
  schedule/completion state for linked work. Do not force an annual agreement
  onto a one-time job or create duplicate visits during bridging.
- **E3.2 — Treatment programs.** Define four/five named treatments with order,
  service windows and optional unequal prices to be agreed in Release 4. Moving
  one round changes only that round. Track complete, skipped and follow-up work
  without rewriting submitted history.
- **E3.3 — Field workflow.** Add appointment windows, minimal crew assignment,
  enforced owner/staff permissions, versioned checklists and a daily stop list
  with manual ordering. Link notes/photos to each job and provide navigation.

Pass when: a rain delay preserves other rounds; a completed visit counts once;
current HVAC entitlements and recurring schedule behavior still work; a staff
member can access only authorized work and cannot access owner financial actions.
Check stale browser submissions, timezone/DST behavior and callbacks/redos.

Code boundary: `internal/servicework`, `internal/visits`, calendar/scheduling
handlers, new job bridge and assignment/checklist records, authentication and
authorization checks. Recovery: stop new job creation if needed, retain readers
and existing recurring/seasonal functionality. Old binaries may ignore new job
types, so verify semantic rollback before reverting.

### Release 4 — Quotes, contracts and billing records

Depends on: E2 measurements and E3 job/program identity. Covers V3.

- **E4.1 — Quoting.** Add package/add-on, fixed, hourly, quantity and tier pricing;
  minimum charges, tax/discount totals, draft/revised/accepted quote states and
  recorded acceptance. Freeze the accepted scope, prices and measurement inputs.
- **E4.2 — Configurable billing terms.** Support the billing patterns below for
  every trade, with shop/service defaults and explicit quote/contract overrides.
  Store the agreed trigger (acceptance, completion, date/period or approved
  milestone), amount calculation and payment terms. Fixed-price contracts have
  an agreed total; ongoing per-visit/time-and-material work can have an open-ended
  total. Never manufacture a fixed annual amount for an ongoing service.
- **E4.3 — Invoice and payment ledger.** Create an invoice once per eligible
  trigger or an agreed group of unbilled visits. Record cash/check/external
  payments, partial payments, early payoff, credits and cancellation adjustments.
  Show billed, paid, unpaid invoices and remaining committed value where known.
  Keep unbilled actual work separate from estimates of future ongoing work.
- **E4.4 — Trigger correctness.** Atomically queue completion-, date- and
  milestone-triggered billing with stable identities and retries. An invoice
  line can be billed only once. Prepaid work and credited deposits offset the
  balance. Explicitly define skipped-work, redo and cancellation policies.
  Changing future billing terms cannot silently revise issued invoices.

| Billing pattern | Example use | Required behavior |
|---|---|---|
| Full payment upfront | Annual lawn program; prepaid detailing package | One agreed charge; included work remains scheduled without further charges. |
| After each visit/treatment | Contracted lawn treatments; recurring detailing | Completion creates the allocated charge; a delay defers that charge. |
| One-time completion billing | Plumbing diagnosis/repair; electrical service call | Invoice approved flat price or recorded time/material quantities after work. |
| Recurring calendar billing | HVAC membership; monthly landscape maintenance | Charge the agreed monthly/quarterly/annual amount independently of visit count. |
| Periodic billing for actual work | Weekly mowing consolidated into a monthly bill | Group completed, unbilled visits once; distinguish variable actual totals from a flat membership fee. |
| Deposit and balance | House washing; equipment installation | Credit the deposit toward the same job total and bill the remaining balance at the agreed event. |
| Milestone/progress billing | Plumbing/HVAC/electrical installation | Bill an agreed amount or percentage after an approved milestone; preserve changes and credits. |
| Fixed installments | Larger service package split across agreed dates | Bill scheduled portions of the agreed total; rescheduled work does not silently move those dates. |

Pricing and collection are separate from these patterns: a fixed/hourly/unit/tier
price determines the amount; payment terms determine when an invoice is payable;
manual payment, hosted payment link or authorized autopay determines collection.
Start with these bounded patterns and supported combinations, such as a deposit
plus milestones. Do not implement arbitrary scripts or assume every combination
is valid. Validate that planned portions of a fixed contract reconcile to its
total, including discounts, taxes, credits and rounding.

Pass when: every row has a behavioral test and an end-to-end example. Cover four-
and five-treatment programs under both choices, fixed installments, an open-ended
per-visit service, grouped monthly actuals, a membership, time/material completion,
and deposit/milestone billing. Test uneven cents, failed/duplicate triggers,
partial/early payment, renewal boundaries and contract changes. Switching between
billing patterns must not rebill work. Existing agreements never become invoices
automatically, and the contract total is never billed on top of its component
charges.

Code boundary: new quoting/billing domain code, agreement/plan references,
completion hooks, private invoice and acceptance surfaces. Audit revenue metrics,
renewal status labels and export visibility. Recovery: suspend issuance, retain
the ledger, reconcile existing records and correct with audited adjustments.

### Release 5 — Application records and weather

Depends on: E2–E3; can be developed independently of Release 4 billing once the
visit/time contracts are stable. Covers V4.

- **E5.1 — Product usage.** Add catalog and label/SDS references; record product,
  quantity/unit, carrier or mix, rate as entered, area, applicator, method and
  actual application start/end. Allow multiple products and preserve corrections.
- **E5.2 — Weather record.** Save manual onsite conditions and an optional
  provider snapshot with coordinates, source and timestamps. Distinguish observed
  conditions from modeled/forecast data. Permit saved work during provider outage
  and label unavailable or stale information.
- **E5.3 — History and export.** Produce an internal application record combining
  relevant fields and photos, with intentionally separate customer summaries.
  Test the pilot's products/state before representing any export as regulatory
  reporting. Recordkeeping supplies no automatic permission to apply chemicals.

Pass when: a treatment is fully recorded during weather failure; later retrieval
shows saved application-time conditions, not today's weather; concentrate and
mixed volume cannot be silently substituted; private details remain private.

Code boundary: new application/weather records and injected weather client,
field-record UI, job links and export. Recovery: disable provider fetches and keep
manual entry/history. Usage records come first; inventory expansion is below.

### Release 6 — Online collection

Depends on: E4 ledger and confirmed payment provider/account setup. Covers V5.

- **E6.1 — Contractor account connection.** Establish the selected provider's
  onboarding, tenant/account mapping and collection permissions. Separate each
  contractor's funds from Sideyard's subscription revenue. Use sandbox accounts.
- **E6.2 — Hosted payment and receipts.** Collect against the correct invoice,
  handle all supported invoice triggers and balances, and produce receipts. Online
  customer communications are reviewed before activation. Automatic collection
  requires customer authorization; sending a bill and auto-charging are distinct.
- **E6.3 — Payment events and recovery.** Verify webhooks, deduplicate requests
  and events, reconcile pending/success/failed payments, refunds and disputes,
  and handle interrupted collection and out-of-order events. Monitor stuck work.

Pass when: two contractor accounts remain isolated, failed/pending payments are
not marked paid, repeated events change the ledger once, and early payoff,
refunds and remaining treatment allocations reconcile. Validate the provider
path in its sandbox before enabling any real collection.

Code boundary: provider adapter, durable outbox/event processing, billing UI,
new narrowly authenticated webhook endpoint and deployment configuration. Keep
the existing renewal test stub isolated until explicitly replaced. Recovery:
disable new collection while keeping incoming events and reconciliation running.

### Release 7 — Measurement connection and six-trade readiness

Depends on: E2 measurements; quote checks need E4; full walkthroughs need the
relevant earlier releases. Covers V6.

- **E7.1 — Reviewed imports.** Add measurement CSV mapping, preview, source IDs,
  bounded validation and repeat-import behavior. Preserve original customer/site
  imports and owner-corrected measurements. Imported changes cannot rewrite an
  accepted quote.
- **E7.2 — Deep Lawn adapter.** Confirm actual API access, coverage, storage rights
  and budget; implement scoped credentials, provider tests, quotas and error
  handling. Manual/imported measurements remain usable while access is pending.
  Track this ticket as deferred if access is unavailable, not completed.
- **E7.3 — Configurability finish.** Add bounded extra fields of the agreed types,
  required/optional settings and record-type scoping. Verify preset update
  previews, mixed trades, archived fields, search/display and export behavior.
- **E7.4 — Trade acceptance and pilot.** Run the six examples below, create
  synthetic demos, then align landing copy with verified capabilities. Plan an
  observed four-week operating pilot; obtain authorization before contacting
  operators or importing their business data.

Pass when: all six trade walkthroughs pass; previewed imports reconcile; provider
failures preserve usable records; demos cannot charge money or trigger paid
provider calls. An unavailable Deep Lawn connection remains a named limitation.

Code boundary: import boundary and tests, measurement adapter, optional-field
definitions/UI, demo fixtures, landing/demo templates and operating docs.
Recovery: disable imports/provider or revert claims while preserving saved data.

## Six trade acceptance examples

| Trade | End-to-end acceptance scenario |
|---|---|
| Detailing | Quote two vehicles, schedule mobile and shop work, record separate condition photos/checklists and payment, then book repeat maintenance. |
| House washing | Quote house plus driveway using the correct surfaces, record water/access details, take a deposit, move for rain, record usage/photos and collect the balance. |
| Landscaping | One four/five-treatment contract, tested with upfront and after-treatment payment; record products/weather, reschedule one round and reconcile the season. |
| HVAC | Two units with separate history, existing spring/fall membership visits, readings/checklists and an independently quoted repair. |
| Plumbing | Diagnose a water heater, quote replacement, record deposit and balance, installation/warranty details and a follow-up. |
| Electrical | Diagnose a circuit issue, quote work, record panel/circuit details and completion photos, then schedule an inspection follow-up without billing it accidentally. |

## Team review

Issue: manual request; no external issue or comment created.
Scope: execution plan for all six verticals and the confirmed buddy workflow.
Priority: P1.

### Product manager

- User problem: owners need relevant defaults and one place to manage field work
  and its money without configuring an entire software system.
- Acceptance: all six walkthroughs and every shared billing pattern; the friend's
  treatment contract is one mandatory example, not a restriction on other trades.
- Out of scope for these releases: broad construction management, payroll, full
  accounting, financing and automatic route optimization.
- Recommendation: release useful customer records first, keep all six presets
  in scope, and verify the friend's workflow before broad marketing claims.

### Architect

- Affected: shop settings, properties/assets, service occurrences, media, quotes,
  billing, applications and provider boundaries.
- Risk: duplicating completion state or conflating a contract with an invoice.
- Migration/rollback: additive schema and retained identifiers; explicitly test
  whether older binaries can still interpret new saved behavior.
- Recommendation: one authoritative visit state and one auditable billing ledger;
  separate contract, completion, invoice and settlement identities.

### Unit-testing expert

- Seams: injected storage, clocks, measurement/weather/payment clients.
- Focus: tenant access, template versions, area units, upload interruption,
  completion retries, prepaid treatment suppression, rounding and early payoff.
- Regression: existing imports, renewal snapshots, seasonal entitlements,
  recurrence/date overrides, public demo and signup notifications.
- Recommendation: use table-driven cases for every billing trigger/collection
  combination, including four/five-treatment contracts, and actual Postgres
  integration tests for atomicity and concurrent writes.

### Staff software engineer

- Approach: typed domain records and small handler/template changes, adding
  packages only where ownership is clear; no unrelated restructuring.
- Edge cases: vehicles at changing locations, callbacks, skipped work, immutable
  accepted quotes, edited measurements and corrections to submitted field records.
- Sequence: freeze identities before adapters; wire new UI only when the backing
  capability exists. Shared presets must not branch the application by trade.
- Recommendation: execute the listed sub-tickets as small reviewable slices.

### DevOps engineer

- Dependencies: private object storage, weather access, payment accounts and
  optional paid measurement API. Existing single-binary deployment remains.
- Checks: isolated secrets/accounts, usage ceilings, upload/queue monitoring,
  backup/restore coverage and exact-revision CI/staging evidence.
- Recovery: disable new external writes independently from record access and
  payment-event ingestion. Keep Pantry and Headscale untouched.
- Recommendation: prove provider failure and restoration paths before activation.

### QA tester

- Evidence: source-plan repository assessment and confirmed user feedback;
  no claim of implemented behavior from this planning review.
- Acceptance: six trade walkthroughs, phone use, real staging routes and provider
  sandbox checks; show the user the result at every release checkpoint.
- Regression risk: internal notes leaking in exports, duplicated visits/bills,
  stale time/weather, photos lost on interruption and demo integrations escaping.
- Recommendation: use synthetic users/data for acceptance and verify privacy,
  totals and history as well as the happy path.

Decision: recommended execution sequence; planning review complete. User approval
to implement has not been given in this request.
Open risks: payment setup (owner + implementation owner), measurement access
(owner + implementation owner), application-record requirements (pilot operator
+ implementation owner), semantic migration/rollback (implementation owner).

## Dependencies, ownership and release checks

One execution owner controls changes, integration, commits and release evidence.
Review perspectives advise at each milestone. This planning turn assigns no
agents and starts no implementation.

Main sequence: **1 → 2 → 3 → 4 → 6**. Release **5** can proceed after **3** once
its visit/time contracts settle. Measurement work in **7** can start after **2**;
its final acceptance follows all relevant earlier capabilities. Provider research
can proceed independently; shared migrations and completion hooks are integrated
by the execution owner in a deliberate order.

For each slice: focused behavioral checks, `go vet ./...`, full tests with
`CLUBDUE_REQUIRE_POSTGRES_INTEGRATION=1` and `DATABASE_URL` set to a disposable
Postgres database, and `CGO_ENABLED=0 go build -o /tmp/clubdue ./cmd/clubdue`.
Use the existing CI production and pilot rehearsals. Apply migration checksum,
old-data and restore checks to data/storage changes; provider sandbox tests to
integrations; phone/desktop browser checks to UI changes.

Once implementation/publishing is authorized: exact-revision CI → staging deploy
→ `./scripts/smoke-test.sh staging` and the release demonstration → show results
and staging URL → obtain production approval under `AGENTS.md` → production
workflow and verification. A failed gate stops promotion and is repaired before
continuing. This plan does not authorize new production deployment.

## Conditional work and follow-on scope

These items are preserved from the source plan rather than silently included in
the seven-release promise:

| Item | Prerequisite / decision |
|---|---|
| Chemical inventory, lot/truck stock and replenishment | After E5 usage records are validated, define receipts/adjustments and reconcile stock; pilot must establish demand. |
| Weather-assisted bulk rescheduling | After E3 date overrides and E5 weather; review proposed moves before applying them and preserve billing triggers. |
| Multi-stop map export and automatic route optimization | After E3 manual stop ordering; validate provider limits and routing requirements before choosing an optimizer. |
| Full offline synchronization | After field-save/retry behavior; needs an explicit conflict-resolution and device-data design. |
| Specialized refrigerant or regulatory reports | Relevant operator/product/jurisdiction requirements and separately verified exports. |

Decisions needed only before their dependent activation: actual payment tool and
whether payment is requested or automatically collected; object-storage choice
and retention; application product/state details; Deep Lawn access/budget; crew
permissions and customer-sharing policy. Use synthetic examples and owner-only
access to make early work reviewable without waiting for all those decisions.

## Original planning handoff

This was the planning-only checkpoint. Implementation has since been authorized;
see [delivery status](SIDEYARD-DELIVERY-STATUS.md) for current completed work,
staging evidence and remaining scope.

Result at that checkpoint: execution planning complete; application work not started.
Commit: none; planning files remain local.
Tests: document coverage, sequence and whitespace checked; application tests not
run because no application code changed.
Staging: https://clubdue-staging.michelindigitalmedia.com (pending for this work;
no new revision or smoke-test result).
Production: not deployed in this planning task.
Next deliverable after implementation is requested: Release 1 business setup,
followed by the Release 2 customer/property demonstration.
