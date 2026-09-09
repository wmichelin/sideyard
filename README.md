# Sideyard

Sideyard is a recurring home-service commitment ledger. It keeps customers,
service locations, plan terms, renewals, and recurring work in one small app
beside a spreadsheet, calendar, billing tool, or field-service suite. The same
neutral core supports the established HVAC workflow and a landscaping
design-partner workflow.

It is a single Go binary with embedded HTML/CSS and Postgres.

## Local demo in five minutes

1. Start Postgres and configure the app:

   ```bash
   docker compose up -d db
   cp .env.example .env
   ```

   The included URL points at `127.0.0.1:5433`; migrations apply automatically when the app starts.

2. Start Sideyard:

   ```bash
   go run ./cmd/clubdue
   # http://127.0.0.1:8080
   ```

3. Seed the walkthrough shop in another terminal:

   ```bash
   curl -X POST http://127.0.0.1:8080/dev/seed
   ```

4. Sign in at [http://127.0.0.1:8080/login](http://127.0.0.1:8080/login) with
   these local-development-only credentials:

   ```text
   owner@demo.local
   demo1234
   ```

`ENABLE_DEV_ROUTES=1` is required for the seed route and is already present in `.env.example`. The app loads `.env` without replacing already-set environment variables.

For a prospect, share the permanent
[one-click landscaping demo](https://getsideyard.com/demo).
Staging keeps an equivalent [release-review demo](https://clubdue-staging.michelindigitalmedia.com/demo).
Each link creates a fresh fictional workspace for that browser, ends access
after four hours, and requires no shared credentials. Scheduled cleanup removes
expired live workspaces; encrypted backups may retain submitted data for up to
30 days, so use only fictional or fully redacted input. Each workspace starts with 30
fictional customer records spanning residential, commercial, multi-location,
seasonal, renewal, and recurring-work examples. It is deliberately unavailable
unless the deployment explicitly enables it.

Without `DATABASE_URL`, Sideyard runs an in-memory legacy sample screen instead of the full agreement workflow.

## Feature tour

Start with the [product map](docs/PRODUCT-MAP.md): a directed page graph, the
features and access available on every page, production/staging availability,
and a client-onboarding checklist for each trade. This is the canonical product
reference; the overview below is a quick introduction.

- **Business setup** — choose one or more of six trades, preview starter service
  names, then customize or disable them. Setup preserves existing agreements and
  schedules; starter services are drafts for future workflows, not priced plans.
- **Property records** — open a customer's property to save internal site details,
  add dated internal notes, record area/length measurements with sources and history, and launch directions
  in Google or Apple Maps. Property records are currently owner-only.
- **Customer vehicles** — owners can keep multiple vehicle records per customer,
  with make/model/year, an optional identifier and internal notes, independent of
  service locations.

- **Dashboard** — active agreements, renewals due this month, lapsed month-to-date, and overdue service commitments, plus a month calendar of open recurring visits and a daily agenda with date-change links.
- **Agreement book** — create customers and service templates, then create or edit a plan with renewal date, status, price, and optional included-service entitlement.
- **Recurring services** — attach daily, every-N-weeks, or monthly work to a plan, optionally end-date it, then pause or resume it without changing renewal terms.
- **CSV import** — export Google Sheets as CSV, inspect a mandatory dry-run, and commit it. Re-imports use `external_id` to update rather than duplicate records. Reordered/extra columns, BOM/CRLF files, common US/ISO dates, blank optional fields, and dollar prices are accepted. Try [`landscaping-google-sheets-template.csv`](internal/app/testdata/landscaping-google-sheets-template.csv).
- **Renewals** — work deterministic 90/60/30/14/7-day tasks with customer contact details, property, agreement links, clear upcoming/due/past-due state, and recorded outcomes. Imported overdue agreements remain in the overdue cycle until an explicit outcome is recorded.
- **Service work** — work recurring landscaping/service occurrences beside the established seasonal HVAC queue; complete, skip, pause, resume, or export work using its actual service name. A generic handoff is not an appointment time.
- **Schedule** — see last recorded completion and next open visit per recurring service, then move an individual visit without shifting the weekly, biweekly, or monthly pattern. See [scheduling behavior](docs/SCHEDULING.md).
- **Homeowner renewals** — from an agreement edit page, create a 30-day renewal link. Staging exposes only a clearly non-charging test stub; production asks the homeowner to contact the shop because online payment is unavailable.

For a guided owner presentation, use [docs/demo-script.md](docs/demo-script.md).
For the researched market thesis, evidence gates, design-partner plan, and
technical evolution path, use
[docs/PRODUCT-MARKET-FIT-PATH.md](docs/PRODUCT-MARKET-FIT-PATH.md).
For the deliberately limited one-owner pilot, its privacy boundaries, onboarding
checklist, feedback cadence, and honest FAQ, see [docs/CONTROLLED-PILOT.md](docs/CONTROLLED-PILOT.md).

## Configuration

| Variable | Purpose |
|----------|---------|
| `APP_ENV` | `development` (default), `staging`, or `production`; staging/production fail closed without their required configuration |
| `DATABASE_URL` | Postgres connection string |
| `SESSION_SECRET` | Required secret for session and homeowner-link hashes |
| `PUBLIC_URL` | HTTPS public URL required in staging and production |
| `ENABLE_DEV_ROUTES` | Set to `1` to enable `POST /dev/seed` locally; rejected by staging and production |
| `ENABLE_PUBLIC_DEMO` | Explicit staging/production opt-in for isolated one-click demos; disabled by default and rejected in development |
| `BOOTSTRAP_EMAIL` / `BOOTSTRAP_PASSWORD` / `BOOTSTRAP_SHOP_NAME` | Optional owner created for an otherwise empty database (`SHOP_NAME` remains a local compatibility alias) |
| `SIGNUP_ALLOWLIST` | Comma-separated staging/production pilot enrollment allowlist; enrollment is closed there when empty |

For audited, operator-run pilot owner provisioning or password recovery, see
[owner provisioning](docs/OWNER-PROVISIONING.md). Never place production owner
credentials in the repository or a GitHub workflow.

## Verify

```bash
go test ./...
go build -o /tmp/clubdue ./cmd/clubdue
curl -fsS http://127.0.0.1:8080/healthz
```

## Deploy

Staging and production on the pantry DO droplet (Pantry/Headscale untouched):

| Env | URL |
|-----|-----|
| Staging | https://clubdue-staging.michelindigitalmedia.com |
| Production | https://getsideyard.com |

Push to `main` → staging auto-deploy. Production is manual after review.

See [docs/DEPLOY.md](docs/DEPLOY.md), [docs/RELEASE-WORKFLOW.md](docs/RELEASE-WORKFLOW.md),
and the [pilot operations runbook](docs/OPERATIONS-RUNBOOK.md).

## MVP boundaries

The staging payment screen is deliberately a test stub: it does not collect card data, charge a card, or mark an agreement paid. Production has no payment CTA and directs homeowners to contact the shop. Stripe Checkout plus a verified webhook remains a future, separately approved integration.

Sideyard does not yet provide route optimization, dispatch, GPS tracking,
invoicing/accounting, automated messaging, or a direct Google Sheets OAuth
connection. The landscaping importer currently expects the documented CSV
profile and a stable source ID. A real design-partner onboarding begins with an
authorized sanitized sheet and assisted reconciliation; it is not safe to
claim arbitrary-sheet compatibility before that evidence exists.

## Public interest page

The public homepage accepts early-access interest at `POST /interest`. Submissions
are stored in Postgres, deduplicated by normalized email, and exported through the
private `export-interest` operator command. See [Sideyard launch](docs/SIDEYARD-LAUNCH.md).

New signups also queue an owner email alert. Delivery requires configured mail
credentials; see [signup notifications](docs/SIDEYARD-NOTIFICATIONS.md) for setup
and the current activation status.
