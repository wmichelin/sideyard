# Product-market-fit and technical path

Research date: September 1, 2026. This is a decision memo and experiment plan,
not a claim that ClubDue has product-market fit.

## Decision

ClubDue should not become another all-in-one landscaping field-service suite.
The initial wedge is narrower:

> For owner-operated recurring-maintenance businesses that have outgrown a
> spreadsheet but do not want a full field-service suite, ClubDue is the
> reliable commitment book: what each property was promised, what is due or
> overdue, what actually happened, and which plans need renewal or repricing.

The first ideal customer is a US residential or light-commercial maintenance
business with an owner plus zero to four workers, roughly 30–300 active service
locations, and an existing combination of Google Sheets, Calendar, and a
separate billing product. Weekly mowing, every-N-weeks property care, monthly
checks, and seasonal work are in scope. Design/build projects, snow dispatch,
chemical compliance, and businesses already happy with a full suite are not.

The landscaping friend is the first design partner. One friend can expose the
workflow and help shape a useful product; one friend cannot establish PMF.

## Why this market and why this wedge

The category is large and mostly small. The 2023 US Census County Business
Patterns dataset reports 117,969 employer establishments in landscaping
services (NAICS 56173); 83,241, or 70.6%, had fewer than five employees. It
does not count nonemployer firms, so it understates solo operators. This proves
market size, not demand for ClubDue. [US Census data](https://data.census.gov/table/CBP2023.CB2300CBP?codeset=naics~56173&g=010XX00US)

Recent operator discussions describe the same transition we want to study:

- One operator managing 20–35 yards wanted a beginner-friendly replacement
  for a homemade Sheet that covered customers, addresses, services, notes,
  completion, and recurring visits. Another described the sheet breaking down
  when a second worker did not record completed cuts. [Lawn-care Sheets discussion](https://www.reddit.com/r/lawncare/comments/1ra893k/anyone_have_a_good_google_sheets_template_for/)
- A solo lawn operator using Google Workspace wanted a specialized CRM but did
  not need invoicing because another product already handled it. [LawnCarePros discussion](https://www.reddit.com/r/LawnCarePros/comments/1kpm3xc/looking_for_software_to_manage_clients/)
- A small landscaper using Calendar, Keep, and Sheets emphasized minimizing
  office time. [Landscaping software discussion](https://www.reddit.com/r/landscaping/comments/1udvuh4/landscaping_software/)

These are anecdotes, not representative survey results. They justify testing
three hypotheses: incomplete field updates, suite fatigue, and high sensitivity
to administrative time.

## Competitive reality

Prices are current as researched and can change.

| Alternative | Current offer | Consequence for ClubDue |
|---|---|---|
| Google Sheets | Familiar, flexible, and usually no incremental cost | This is the default competitor. The product must create trust without taking away exportability or demanding software administration. |
| Yardbook | Free Starter already includes customers, scheduling, routing, estimates, invoices, payments, reports, and employee access; paid tiers are $34.99 and $49.99 per month | “Cheaper landscaping software” is not a viable position. [Pricing](https://www.yardbook.com/pages/pricing), [features](https://www.yardbook.com/pages/features) |
| Jobber | Core is $49 month-to-month or $29/month billed annually for one user; higher tiers add reminders, payments, checklists, and accounting integrations | A strong all-in-one SMB incumbent with recurring jobs and landscaping workflows. [Pricing](https://www.getjobber.com/pricing/), [landscaping](https://www.getjobber.com/industries/landscaping-software/) |
| Housecall Pro | Basic starts at $79 monthly or $59/month billed annually and includes scheduling, quotes, invoicing, payments, booking, and dispatch | Another credible general suite; ClubDue should win on focus and setup cost, not breadth. [Pricing](https://www.housecallpro.com/pricing/), [lawn care](https://www.housecallpro.com/industries/lawn-care-software/) |
| LMN | Starter is $297/month for companies under ten employees; higher tiers add deeper estimating and job costing | Attractive when financial and crew complexity dominate, but too heavy for many Sheets-run operators. [Pricing](https://granum.com/lmn/pricing/) |
| Aspire / ServiceTitan | Quote-led, implementation-heavy platforms with broad operations, contracts, renewals, invoicing, and reporting | Upper-market benchmarks, not the initial competitive set. [Aspire plans](https://www.youraspire.com/aspire-plans), [Aspire product](https://www.youraspire.com/products/aspire), [ServiceTitan products](https://www.servicetitan.com/products) |

CSV import alone is not differentiation. Jobber maps headings and previews
errors, while Yardbook accepts CSV/Excel customer imports. [Jobber client import](https://help.getjobber.com/en/articles/import-clients/),
[Jobber recurring-job import](https://help.getjobber.com/en/articles/import-jobs/),
[Yardbook import](https://support.yardbook.com/faq-i-want-to-import-customers-from-csv-or-excel-files/)

The proposed differentiation is exception-first assurance: the owner sees the
commitments at risk, records a field outcome once, and no longer reconstructs
the truth from several tools at the end of the week.

## Jobs to be done

- When planning work, show which promised services are upcoming, due,
  overdue, paused, completed, or skipped.
- When work happens or cannot happen, capture the outcome from a phone before
  context is lost.
- When a season or agreement changes, identify customers requiring restart,
  renewal, cancellation, or price review.
- When leaving Sheets, preserve source identity, explain every rejected row,
  support safe correction/re-import, and keep a clean export.
- Give the owner confidence that nobody was forgotten without turning the
  owner into a software administrator.

## What this delivery establishes

The staging product now has a trade-neutral, additive recurring-service
foundation alongside the existing HVAC workflow:

- The established `Shop -> Customer -> Property -> Agreement -> Renewal` core
  remains intact.
- An agreement can own daily, every-N-weeks, monthly, or end-dated service
  schedules.
- A deterministic generator materializes a bounded 30-day history and 90-day
  future horizon. A unique database constraint makes retries idempotent.
- “Service work” combines the new recurring occurrences with established
  spring/fall visit dues without rewriting or risking the existing workflow.
- Recurring work can be completed, skipped, paused, resumed, and exported with
  its actual service name.
- A Google Sheets CSV export accepts reordered/extra columns, UTF-8 BOM and
  CRLF files, blank optional contact/plan fields, common US or ISO dates, and
  dollar or cent prices. Preview remains mandatory before commit, and stable
  `external_id` values make re-imports update rather than duplicate.
- A permanent staging `/demo` entry creates a fresh fictional landscaping
  tenant and a four-hour browser session without putting credentials or tokens
  in the URL. The deterministic baseline contains 30 fictional customer records,
  residential and light-commercial plans, multi-location accounts, contact
  gaps, lifecycle states, recurring outcomes, seasonal work, and renewal work;
  active schedules are deliberately capped so the unpaginated queue stays
  usable. Each visitor is isolated and can use the ordinary product forms;
  the sandbox admits at most 30 state-changing requests, caps encoded forms at
  16 KiB and individual values at 2,000 bytes, and permits only a bounded
  `/import` preview and commit for one fictional or redacted `.csv`. The file is
  capped at 100 data rows, 128 KiB raw, and 2,000 bytes per cell, with 500
  admitted rows and a combined 20,000 projected recurring visits across
  committed imports over the sandbox lifetime. Preview creates no product
  records and holds the exact CSV server-side for at most 15 minutes behind an
  expiring, tenant-bound, single-use commit token; preview and
  commit each count toward the 30-action budget. All other multipart remains
  blocked. At four hours the tenant stops resolving and cleanup deletes it by
  cascade. Production refuses the feature flag and does not register the route.
- The mobile navigation is compact enough that the work view is visible in the
  initial phone viewport.

This is a reliable concept and design-partner foundation, not yet a claim that
an arbitrary real landscaping Sheet can be onboarded unattended. The friend’s
authorized, sanitized sheet is still required to validate identifiers,
multiple locations/services, formulas, cadence conventions, prices, exceptions,
and any billing/calendar dependencies.

## Evidence gates

Do not expand toward a full suite merely because prospects request features.
Advance only when behavior clears these gates:

1. At least five of eight qualified interviews report a recent missed,
   duplicate, late, or undocumented recurring service and rank it among their
   top three operational pains.
2. At least three owners provide authorized, sanitized real sheets.
3. Three owners reconcile at least 90% of active recurring accounts in under
   15 assisted minutes.
4. At least three of five pilots use the due-work loop for four consecutive
   weeks.
5. At least 80% of due occurrences receive an outcome, with no critical lost
   or duplicate records.
6. At least two owners actually pay; hypothetical willingness does not count.
7. Owners choose ClubDue as the operational commitment source of truth while
   retaining a safe export.

If interviews instead show that routing, invoicing, or payments are the primary
problem, integrate with or defer to an incumbent rather than building another
field-service platform.

## Design-partner plan

### Week 0: observe before configuring

- Watch the friend operate one real workweek.
- Ask for an authorized sanitized copy with formulas converted to values and
  customer PII replaced.
- Record every tab, identifier, recurrence, status, price basis, exception,
  and handoff to Calendar, invoicing, or accounting.
- Walk through the last missed, duplicated, delayed, or undocumented service.
- Agree on the single source of truth and a rollback/export procedure.

### Week 1: concierge onboarding

- Map the sanitized sheet to the current CSV profile; do not silently guess
  ambiguous headings such as “date,” “amount,” or “status.”
- Preview and reconcile counts before any commit.
- Configure the smallest representative set of weekly, biweekly, monthly, and
  seasonal services.
- Have the owner perform the dashboard -> service work -> outcome loop from a
  phone without coaching.
- Compare the ClubDue queue to the Sheet every day and record discrepancies.

### Weeks 2–5: measured pilot

- Run the commitment loop for four consecutive weeks.
- Review unresolved work, correction/re-import behavior, and manual minutes
  weekly.
- Measure import reconciliation, occurrence outcome coverage, duplicate/lost
  record defects, return use, and the concrete loss or time saved.
- Ask for payment only after the workflow prevents a visible loss or replaces
  repeated reconciliation.

## Product sequence

### Next: import integrity and real-sheet fit

Build a narrow tabular import boundary shared by CSV today and a future Sheets
connector. Add separate customer, property, agreement, schedule, and occurrence
source IDs; saved per-shop mappings; durable preview batches; row/error limits;
and an atomic, single-use commit. Preview must display the exact persisted
payload that commit will apply. This removes the current legacy restriction of
one source ID serving as all entity identities and avoids returning an entire
base64-encoded sheet through the browser.

### Then: follow the observed bottleneck

- If fulfillment failures dominate: day grouping, generic route handoff, crew
  assignment, weather-aware bulk rescheduling, and photos.
- If retention dominates: seasonal restarts, price-review tasks, renewal
  acceptance, and reminders.
- If billing dominates: integrate QuickBooks, Stripe, or the design partner’s
  existing billing product before considering accounting features.
- If none creates repeated use and payment, stop expanding the feature set.

### Explicit non-goals

- Route optimization, dispatch board, GPS tracking, payroll, timesheets, or
  job costing.
- Invoicing, accounting, or card collection.
- Automated email/SMS or public self-service enrollment.
- Chemical-compliance records, design/build project management, or snow-event
  dispatch.
- An arbitrary workflow engine, RRULE parser, or generic custom-field/EAV
  framework.

## Technical architecture

### Stable core and bounded capabilities

Keep the core neutral and add bounded capabilities around it:

```text
Shop
  -> Customer -> Property/service location
  -> Agreement/plan terms -> Renewal cycle
                           -> Service schedule -> Service occurrence/outcome
```

Contract terms, service recurrence, generated outcomes, and billing cadence are
separate concepts. The current additive schedule tables deliberately coexist
with legacy `visit_dues`; convergence should happen only after pilot evidence
shows the common semantics. Avoid trade-specific forks.

### Data correctness and rollback

- Every query and mutation carries `shop_id`; triggers reject schedules or
  occurrences pointing across tenants.
- Unique `(shop_id, schedule_id, due_on)` occurrence identity makes generation
  and restarts idempotent.
- Outcomes are final and transactionally serialized; a retry of the same
  outcome is harmless, while a conflicting rewrite is rejected.
- Migrations add tables and defaulted columns only. The retained previous
  binary ignores them, so application rollback does not require destructive
  schema reversal.
- Continue adding composite tenant ownership constraints as the model evolves.
  PostgreSQL multicolumn foreign keys are the preferred invariant. [PostgreSQL constraints](https://www.postgresql.org/docs/current/ddl-constraints.html)

### Google Sheets path

CSV mapping comes before OAuth because it tests value with much less privacy
and operational risk. A later direct connector should use Google Picker and
`drive.file` per-file access, not blanket spreadsheet access; read bounded
ranges with `values.batchGet`, use request deadlines, and apply capped backoff
for 429/503 responses. Store refresh tokens encrypted under a dedicated,
versioned connector key rather than `SESSION_SECRET`.

References: [Sheets scopes](https://developers.google.com/workspace/sheets/api/scopes),
[Drive per-file authorization](https://developers.google.com/workspace/drive/api/guides/api-specific-auth),
[values.batchGet](https://developers.google.com/workspace/sheets/api/reference/rest/v4/spreadsheets.values/batchGet),
[Sheets error handling](https://developers.google.com/workspace/sheets/api/troubleshoot-api-errors).

### Demo safety and reliability

`GET /demo` is no-store and never creates a session. A CSRF-protected POST
creates the isolated workspace, then sets the existing opaque Secure,
HttpOnly, SameSite cookie. No password, bearer token, or session ID appears in
the URL; URL session identifiers can leak through history, logs, bookmarks,
referrers, and indexing. [OWASP session guidance](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)

The staging flag is the kill switch. Active demo workspaces are capped and use
fictional data. Ordinary setup and workflow forms write only to the visitor's
isolated tenant. Every admitted state-changing request consumes one of 30
durable sandbox slots—even if product validation later rejects it—and is
limited to a 16 KiB URL-encoded form with no field value over 2,000 bytes.

The sole upload boundary is the exact `/import` preview-and-commit flow. Preview
accepts one `.csv` containing only fictional or redacted data, with at most 100
data rows, 128 KiB of raw CSV, and 2,000 bytes in any cell. Across admitted
commits, a sandbox may import at most 500 data rows and admit a combined 20,000
projected recurring visits during its lifetime. Preview creates no customer,
property, agreement, or service records; it holds the exact CSV server-side for
at most 15 minutes. Commit requires an expiring token bound to that tenant and
exact preview, and the token can be used only once. Preview
and commit each consume one of the 30 sandbox action slots. All other multipart
uploads remain unavailable.

The session and tenant expire together after four hours. Expired workspaces no
longer authenticate or resolve public renewal links, and cleanup deletes the
tenant and its child rows through the scoped cascade. Production configuration
rejects the flag and `/demo` remains 404 until a separate, explicitly approved
production design exists.

### Reliability gates for every release

- Unit tests plus required Postgres integration tests on a fresh schema.
- Migration checksum verification and a previous-binary compatibility check.
- Static build, shell syntax checks, and production-mode rehearsal proving
  public demo/development routes are absent.
- Exact-commit CI and staging deploy success.
- Staging smoke for one-click demo creation, bounded ordinary form writes,
  bounded `/import` CSV preview and commit, rejection of every other multipart
  upload, cross-visitor isolation, dashboard, work outcomes, export, liveness,
  and readiness. A failed smoke automatically rolls staging traffic back only
  to a release carrying the demo-sandbox safety marker; unsafe legacy rollback
  is refused.
- Desktop and phone browser acceptance, with request/log review and no secrets
  or customer data in evidence.
- Explicit user approval before any production deployment.

## Questions for interviews

- Show every tool used from accepting a customer through getting paid. Where
  do two sources disagree?
- Walk through the last missed, duplicate, delayed, or undocumented service.
- How are weekly, biweekly, monthly, seasonal, paused, and customer-specific
  schedules represented?
- Who records completion, when, and from which device? How long does it take?
- What property facts must a worker know: access, pets, gates, scope, photos?
- How do service cadence, billing cadence, season, and price-review timing
  differ?
- Are customers on formal terms, rolling service, or informal arrangements?
- What was tried besides Sheets, and why was it stopped?
- What is valuable about the current Sheet and must not be lost?
- After a hands-on pilot, will the owner commit to four weeks of real use and a
  paid continuation?
