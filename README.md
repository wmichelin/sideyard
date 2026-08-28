# ClubDue

ClubDue is an HVAC/home-services maintenance-agreement ledger. It keeps the agreement book, renewal work, seasonal visits, and an import-friendly homeowner list in one small app beside Jobber or a spreadsheet.

It is a single Go binary with embedded HTML/CSS and Postgres.

## Local demo in five minutes

1. Start Postgres and configure the app:

   ```bash
   docker compose up -d db
   cp .env.example .env
   ```

   The included URL points at `127.0.0.1:5433`; migrations apply automatically when the app starts.

2. Start ClubDue:

   ```bash
   go run .
   # http://127.0.0.1:8080
   ```

3. Seed the walkthrough shop in another terminal:

   ```bash
   curl -X POST http://127.0.0.1:8080/dev/seed
   ```

4. Sign in at [http://127.0.0.1:8080/login](http://127.0.0.1:8080/login):

   ```text
   owner@demo.local
   demo1234
   ```

`ENABLE_DEV_ROUTES=1` is required for the seed route and is already present in `.env.example`. The app loads `.env` without replacing already-set environment variables.

Without `DATABASE_URL`, ClubDue runs an in-memory legacy sample screen instead of the full agreement workflow.

## Feature tour

- **Dashboard** — active agreements, renewals due this month, lapsed month-to-date, and overdue visits, with direct links to the work queues.
- **Agreement book** — create customers and plan templates, then create or edit an agreement with renewal date, status, price, and entitled/used visits.
- **CSV import** — upload a fixed-format file, inspect a dry-run, and commit it. Re-imports use `external_id` to update rather than duplicate records. Try [`testdata/sample-import.csv`](testdata/sample-import.csv).
- **Renewals** — view active agreements in 7/14/30/60/90-day windows.
- **Visits** — track seasonal spring/fall due work, bulk mark it scheduled or completed, and export a Jobber-ready CSV.
- **Homeowner renewals** — from an agreement edit page, create a 30-day renewal link. Its payment button is intentionally a non-charging Stripe/webhook placeholder for this MVP.

For a guided owner presentation, use [docs/demo-script.md](docs/demo-script.md).

## Configuration

| Variable | Purpose |
|----------|---------|
| `DATABASE_URL` | Postgres connection string |
| `SESSION_SECRET` | Required secret for session and homeowner-link hashes |
| `ENABLE_DEV_ROUTES` | Set to `1` to enable `POST /dev/seed` locally |
| `BOOTSTRAP_EMAIL` / `BOOTSTRAP_PASSWORD` | Optional owner created for an otherwise empty database |
| `SHOP_NAME` | Shop name used for the optional bootstrap owner |

## Verify

```bash
go test ./...
go build ./...
curl -fsS http://127.0.0.1:8080/healthz
```

## Deploy

Staging and production on the pantry DO droplet (Pantry/Headscale untouched):

| Env | URL |
|-----|-----|
| Staging | https://clubdue-staging.michelindigitalmedia.com |
| Production | https://clubdue.michelindigitalmedia.com |

Push to `main` → staging auto-deploy. Production is manual after review.

See [docs/DEPLOY.md](docs/DEPLOY.md) and [docs/RELEASE-WORKFLOW.md](docs/RELEASE-WORKFLOW.md).

## MVP boundaries

The payment screen is deliberately a test stub: it does not collect card data, charge a card, or mark an agreement paid. Production Stripe Checkout plus a verified webhook remains the next payment integration step.
