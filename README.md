# ClubDue

HVAC/home-services **maintenance agreement ledger** — track plans, renewals, and visits beside Jobber or spreadsheets.

Single Go binary with embedded HTML/CSS and Postgres (~10 MB Docker image).

## Run locally

### 1. Start Postgres

```bash
docker compose up -d db
```

Postgres listens on **127.0.0.1:5433** (user/pass/db: `clubdue`).

### 2. Configure environment

```bash
cp .env.example .env
```

Key variables:

| Variable | Purpose |
|----------|---------|
| `DATABASE_URL` | `postgres://clubdue:clubdue@127.0.0.1:5433/clubdue?sslmode=disable` |
| `SESSION_SECRET` | Cookie signing secret |
| `ENABLE_DEV_ROUTES` | Set `1` to enable `POST /dev/seed` |
| `BOOTSTRAP_EMAIL` / `BOOTSTRAP_PASSWORD` | Optional first-shop owner on empty DB |

The app loads `.env` on startup (does not override existing shell env vars).

### 3. Run the app

```bash
go run .
# http://127.0.0.1:8080
```

Migrations in `migrations/` apply automatically on startup.

### 4. Seed demo data (optional)

```bash
curl -X POST http://127.0.0.1:8080/dev/seed
# owner@demo.local / demo1234
```

Then sign in at `/login` and open `/dashboard` or `/agreements`.

Without `DATABASE_URL`, the app runs in legacy demo mode (in-memory sample data).

## Deploy

Staging and production on the pantry DO droplet (Pantry/Headscale untouched):

| Env | URL |
|-----|-----|
| Staging | https://clubdue-staging.michelindigitalmedia.com |
| Production | https://clubdue.michelindigitalmedia.com |

Push to `main` → staging auto-deploy. Production is manual after review.

See [docs/DEPLOY.md](docs/DEPLOY.md) and [docs/RELEASE-WORKFLOW.md](docs/RELEASE-WORKFLOW.md).

## Phase 1 scope

- Agreement book (customers, plan templates, agreements)
- Shop-scoped auth (signup/login, session cookie)
- Renewal queue (90/60/30/14/7 day buckets)
- CSV import, Stripe billing, visit due-list (planned)
