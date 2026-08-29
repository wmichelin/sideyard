# Deployment

ClubDue runs on the **same DigitalOcean droplet as Pantry**, in isolated containers:

| Env | URL | Container | Port | Image tag |
|-----|-----|-----------|------|-----------|
| **Staging** | https://clubdue-staging.michelindigitalmedia.com | `clubdue-staging` | `127.0.0.1:8082` | `:staging` |
| **Production** | https://clubdue.michelindigitalmedia.com | `clubdue` | `127.0.0.1:8083` | `:latest` |

Pantry (`:8080`) and Headscale (`127.0.0.1:8081`) are untouched.

## Production contract (prepared, not provisioned)

Production remains unprovisioned and this document is not approval to deploy.
After explicit chat approval and the GitHub production-environment review, the
approved workflow uses only these production-specific resources:

| Resource | Production value | Staging equivalent |
|----------|------------------|--------------------|
| Docker network | `clubdue-production-net` | `clubdue-staging-net` |
| Postgres container | `clubdue-production-db` | `clubdue-staging-db` |
| Postgres volume | `clubdue-production-pgdata` | `clubdue-staging-pgdata` |
| App container / local port | `clubdue` / `127.0.0.1:8083` | `clubdue-staging` / `127.0.0.1:8082` |
| Root-only environment file | `/opt/clubdue/production.env` | `/opt/clubdue/staging-db.env` |

Never copy the staging environment file, database credentials, session secret,
network, or volume into production. The production file must be owned by root
and mode `0600`; it is created manually on the droplet, never by deploy code:

```text
POSTGRES_PASSWORD=<generated-long-random-value>
SESSION_SECRET=<generated-long-random-value>
```

The deployment helper rejects ports `8080` and `8081` before any Docker,
registry, or package action. Production cannot be deployed by this run; both
explicit chat approval and the GitHub Actions production gate remain required.

### Production smoke credentials

Create an approved pilot owner with the documented one-off provisioning command
only after the production database exists. Store that owner's smoke credentials
as GitHub **environment secrets** named `CLUBDUE_PRODUCTION_SMOKE_EMAIL` and
`CLUBDUE_PRODUCTION_SMOKE_PASSWORD`. They are supplied only to the production
workflow's smoke step, never checked into this repository, emitted in logs, or
used by staging.

### Encrypted Postgres backups and restore rehearsal

Production backups use AES-256-CBC with PBKDF2, a secret supplied at runtime,
and a root-only destination. Keep the backup passphrase in the approved secret
manager; restrict read access to the backup operator and designated recovery
owner. Do not place the passphrase in the production environment file, shell
history, CI variables, issue comments, or source control.

```bash
# On the production droplet only after explicit production approval.
umask 077
export CLUBDUE_BACKUP_DB_CONTAINER=clubdue-production-db
export CLUBDUE_BACKUP_DIR=/var/backups/clubdue
export CLUBDUE_BACKUP_PASSPHRASE="$(approved-secret-manager-command)"
export CLUBDUE_BACKUP_RETENTION_DAYS=30
./scripts/postgres-backup.sh

# Restore only into a separately created disposable recovery container.
export CLUBDUE_RESTORE_DB_CONTAINER=clubdue-production-restore-rehearsal-db
./scripts/postgres-restore.sh /var/backups/clubdue/clubdue-postgres-<timestamp>.sql.enc
```

`postgres-backup.sh` writes files with `0600` under the explicit backup
directory and deletes only matching encrypted backups older than the configured
retention period. The backup operator must remove expired remote copies and
verify provider-side deletion according to the pilot retention policy. Restore
rehearsals must use a disposable, isolated database and must be destroyed after
verification; never restore a backup into staging or overwrite production.

Run the same self-cleaning, no-remote-infrastructure rehearsal locally or in CI:

```bash
./scripts/production-rehearsal.sh
```

It starts only `clubdue-production-rehearsal-*` Docker resources, verifies
production-mode readiness, CSRF-aware owner login, authenticated dashboard,
encrypted backup output, successful restore to a fresh Postgres container, and
the reserved-port deploy guard. It removes its containers, network, and
temporary backup data on exit.

## Staging database and demo access

Staging automatically provisions an isolated `clubdue-staging-db` Postgres container, the `clubdue-staging-pgdata` persistent volume, and the `clubdue-staging-net` network. Its generated database password and session secret stay in a root-only droplet file and are never committed or logged. Production is unchanged.

After staging deploys, use the real application login at [https://clubdue-staging.michelindigitalmedia.com/login](https://clubdue-staging.michelindigitalmedia.com/login):

```text
owner@demo.local
demo1234
```

The staging smoke test idempotently seeds this demonstration shop through the staging-only dev route.

## One-time setup

### 1. DNS (Namecheap)

In Namecheap → **Domain List → michelindigitalmedia.com → Advanced DNS**, add A records pointing at the pantry droplet IP (`45.55.214.46`):

| Host | Value |
|------|-------|
| `clubdue-staging` | droplet IP |
| `clubdue` | droplet IP |

Confirm:

```bash
dig +short clubdue-staging.michelindigitalmedia.com
dig +short clubdue.michelindigitalmedia.com
```

### 2. GitHub repo secrets

**Settings → Secrets and variables → Actions** on `wmichelin/clubdue`:

| Secret | Value |
|--------|-------|
| `DROPLET_HOST` | Pantry droplet IP |
| `DROPLET_SSH_KEY` | Private key authorized for `root@` droplet (same as Pantry) |
| `GHCR_PAT` | Classic PAT with `read:packages` + `write:packages` |

### 3. Production environment (approval gate)

**Settings → Environments → New environment → `production`**

Enable **Required reviewers** and add yourself. The **Deploy Production** workflow uses this environment so prod deploys wait for your OK in GitHub (in addition to chat sign-off).

## Workflows

| Workflow | Trigger | What it does |
|----------|---------|--------------|
| **CI** | PR + push to `main` | `go test`, `go build` |
| **Deploy Staging** | Push to `main` | Build `:staging`, deploy, smoke-test |
| **Deploy Production** | Manual (`workflow_dispatch`) | Promote `:staging` → `:latest`, deploy, smoke-test |

### Release flow (you + agent)

```
main push ──► staging auto-deploy ──► agent smoke-tests ──► show you staging URL
                                                              │
                                         you OK in chat ◄─────┘
                                              │
                                              ▼
                              trigger Deploy Production (GitHub approval)
                                              │
                                              ▼
                                    https://clubdue.michelindigitalmedia.com
```

1. **Develop aggressively** — merge to `main`; staging updates within minutes.
2. **Agent verifies** — runs `./scripts/smoke-test.sh staging` and checks the live site.
3. **You review** — visit https://clubdue-staging.michelindigitalmedia.com and say OK (or request changes).
4. **Ship prod** — agent or you runs **Deploy Production** (promotes the tested `:staging` image). GitHub environment approval required if configured.

Promote a specific commit instead of current staging:

```bash
gh workflow run deploy-production.yml -f promote_tag=<full-git-sha>
```

## Local deploy

```bash
cp .env.example .env   # fill in DROPLET_HOST + GITHUB_TOKEN
./deploy.sh staging    # safe, frequent
./deploy.sh production # prefer GitHub Actions after staging sign-off
```

## Smoke tests

```bash
./scripts/smoke-test.sh staging
./scripts/smoke-test.sh production
```

Checks `/healthz`, dashboard HTML, and sample data.

See also [RELEASE-WORKFLOW.md](./RELEASE-WORKFLOW.md) for agent instructions.
