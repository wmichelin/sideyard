# Deployment

ClubDue runs on the **same DigitalOcean droplet as Pantry**, in isolated containers:

| Env | URL | Container | Port | Image tag |
|-----|-----|-----------|------|-----------|
| **Staging** | https://clubdue-staging.michelindigitalmedia.com | `clubdue-staging-current` | `127.0.0.1:8082` or `:18082` | `:staging` |
| **Production** | https://clubdue.michelindigitalmedia.com | `clubdue-current` | `127.0.0.1:8083` or `:28083` | promoted `:latest`; deployed by reviewed immutable digest |

Pantry (`:8080`) and Headscale (`127.0.0.1:8081`) are untouched.

## Production contract

This document is not approval to deploy. After explicit chat approval and the
production gate, the approved workflow uses only these production-specific
resources:

| Resource | Production value | Staging equivalent |
|----------|------------------|--------------------|
| Docker network | `clubdue-production-net` | `clubdue-staging-net` |
| Postgres container | `clubdue-production-db` | `clubdue-staging-db` |
| Postgres volume | `clubdue-production-pgdata` | `clubdue-staging-pgdata` |
| App containers / local ports | `clubdue-current` + one `clubdue-previous` / `127.0.0.1:8083` and `:28083` | `clubdue-staging-current` + one `clubdue-staging-previous` / `127.0.0.1:8082` and `:18082` |
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

### Reversible ClubDue application rollout

Each normal deployment preserves the environment database and changes only the
ClubDue application tier. It retains one `*-previous` rollback release on the
alternate port. Before the next candidate starts, that stale rollback slot is
removed to free the alternate port; the current release continues serving.
The new `*-candidate` must then return `ok` from `GET /readyz` before the
single, identified ClubDue nginx vhost upstream changes. The old active
container is then retained as the next `*-previous` release. If candidate
startup fails, traffic remains on the active release, but the stale rollback
slot is no longer available.

A stopped `*-candidate` left by a failed Docker start is removed automatically
on the next retry. A running candidate still fails closed for investigation.

The initial rollout accepts the legacy stable names (`clubdue` or
`clubdue-staging`) on the base port and converts them into the `*-previous`
slot after the candidate is live. It does not create or rewrite an arbitrary
nginx site: the expected `/etc/nginx/sites-available/<domain>` file must be
enabled through the matching `sites-enabled` link, contain the matching
`server_name`, and contain exactly one ClubDue proxy upstream. A Certbot HTTP
redirect plus HTTPS proxy may legitimately produce two `server_name` entries.
Any mismatch,
nginx validation failure, or candidate readiness failure aborts before the
active proxy is changed.

Neither deployment nor rollback removes or recreates an existing ClubDue
Postgres container or volume. A stopped existing database is restarted; an
unhealthy one stops the rollout for investigation. Pantry (`:8080`) and
Headscale (`:8081`) are never candidate, active, or rollback ports.

#### Approved production rollback

Production rollback is a separate, human-approved recovery action. It does not
restore a database or pull an image. After obtaining the same explicit chat and
GitHub production-environment approvals required for a deploy, dispatch the
reviewed **Deploy Production** workflow with `mode=rollback`. The workflow
passes `CLUBDUE_DEPLOY_MODE=rollback`, `CONTAINER=clubdue`, `PORT=8083`, and
`DOMAIN=clubdue.michelindigitalmedia.com` only to the reviewed helper. It
verifies the retained `clubdue-previous` container's `/readyz`, changes only
the ClubDue nginx proxy to that retained alternate port, then swaps the
`current`/`previous` names so a forward recovery remains possible. It fails
closed if either slot or the safe vhost contract is absent. Record active and
retained image IDs, both local ports, the reason, and the recovery result in
the release record.

Use a fix-forward deployment when possible. Roll back only the application
release when database migrations remain backward-compatible; a destructive or
non-backward-compatible schema migration requires a separately rehearsed
database recovery decision.

### Production smoke credentials

Create an approved pilot owner with the documented one-off provisioning command
only after the production database exists. Store that owner's smoke credentials
as GitHub **environment secrets** named `CLUBDUE_PRODUCTION_SMOKE_EMAIL` and
`CLUBDUE_PRODUCTION_SMOKE_PASSWORD`. They are supplied only to the production
workflow's smoke step, never checked into this repository, emitted in logs, or
used by staging.

### Encrypted Postgres backups and restore rehearsal

Production backups use AES-256-CBC with PBKDF2 plus a distinct SHA-256 HMAC
key, both supplied at runtime, and a root-only destination. Each encrypted
`*.sql.enc` file has a required raw 32-byte `*.sql.enc.mac` sidecar. Restore
verifies that sidecar before decrypting or opening a database connection.

Keep the encryption passphrase and HMAC key as separate secrets in the approved
secret manager. Restrict their read access to the backup operator and designated
recovery owner. Do not place either secret in the production environment file,
shell history, CI variables, issue comments, or source control. The scripts
give the HMAC key to OpenSSL through an inherited file descriptor rather than
a command-line argument or temporary key file.

```bash
# On the production droplet only after explicit production approval.
umask 077
export CLUBDUE_BACKUP_DB_CONTAINER=clubdue-production-db
export CLUBDUE_BACKUP_DIR=/var/backups/clubdue
export CLUBDUE_BACKUP_PASSPHRASE="$(approved-secret-manager-command)"
export CLUBDUE_BACKUP_HMAC_KEY="$(approved-hmac-secret-manager-command)"
export CLUBDUE_BACKUP_RETENTION_DAYS=30
./scripts/postgres-backup.sh

# Restore only into a separately created disposable recovery container.
export CLUBDUE_RESTORE_DB_CONTAINER=clubdue-production-restore-rehearsal-db
./scripts/postgres-restore.sh /var/backups/clubdue/clubdue-postgres-<timestamp>.sql.enc
```

`postgres-backup.sh` writes both files with `0600` under the explicit backup
directory and deletes only matching encrypted backups and their MAC sidecars
older than the configured retention period. Store, replicate, retain, and
delete each pair together: a backup without its matching sidecar is
intentionally unrestorable. The backup operator must remove expired remote
copies and verify provider-side deletion according to the pilot retention
policy. Restore rehearsals must use a disposable, isolated database and must
be destroyed after verification; never restore a backup into staging or
overwrite production.

Rotate the encryption passphrase and HMAC key together only after a successful
isolated restore rehearsal. Retain the old pair in the secret manager until
every backup encrypted/authenticated with it expires or is deliberately
destroyed; otherwise older backups cannot be recovered. Record the rotation
date, key identifiers (never key material), backup range, and rehearsal result
in the operator recovery record.

Run the same self-cleaning, no-remote-infrastructure rehearsal locally or in CI:

```bash
./scripts/production-rehearsal.sh
```

It starts only `clubdue-production-rehearsal-*` Docker resources, verifies
production-mode readiness, explicit public-demo opt-in, isolated demo writes,
CSRF-aware owner login, and authenticated dashboard sessions remain active and
separate from demo traffic after a second login for the same account,
encrypted backup output, successful restore to a fresh Postgres container,
rejection of tampered ciphertext and a tampered MAC, the reserved-port deploy
guard, and Docker-isolated candidate/promotion/rollback mapping. It removes
its containers, network, and temporary backup data on exit.

## Staging database and public demo access

Staging automatically provisions an isolated `clubdue-staging-db` Postgres container, the `clubdue-staging-pgdata` persistent volume, and the `clubdue-staging-net` network. Its generated database password and session secret stay in a root-only droplet file and are never committed or logged. Production uses its separate resources listed above.

The preferred prospect entry point is the permanent
[production one-click landscaping demo](https://clubdue.michelindigitalmedia.com/demo).
The [staging demo](https://clubdue-staging.michelindigitalmedia.com/demo) is the
equivalent release-review surface.
`GET /demo` is a no-store entry page; its CSRF-protected POST creates a fresh,
fictional `demo_sandbox` tenant and a normal opaque session that expires with
the workspace after four hours. The workspace starts with 30 deterministic,
fictional customer records and a bounded mix of recurring, seasonal, and
renewal work. Ordinary customer, property, plan, agreement,
recurring-service, renewal, and outcome forms remain interactive inside that
isolated tenant; separate visitors cannot read or change one another's demo.
Each sandbox admits at most 30 state-changing requests, including requests that
later fail product validation. An admitted URL-encoded form must be at most 16
KiB and each field value at most 2,000 bytes. The only upload exception is the
exact `/import` preview-and-commit flow: preview accepts one `.csv` containing
fictional or redacted data, at most 100 data rows, at most 128 KiB raw, and no
cell over 2,000 bytes. A sandbox may admit at most 500 uploaded data rows across
its lifetime. Across committed imports, it may also admit a combined 20,000
projected recurring visits. Preview creates no customer, property, agreement,
or service records; it holds the exact CSV server-side for at most 15 minutes
behind an expiring, tenant-bound, single-use commit token. Preview and commit
each consume one of the 30 saved-action slots. Every other multipart upload
remains blocked. Active
sandboxes are capped; at four hours the session and workspace stop resolving,
the commit token is no longer usable, and scheduled cleanup subsequently
deletes the expired tenant and its child rows through the scoped cascade. Encrypted
production backups may retain submitted data for up to 30 days, so demo input
must always be fictional or fully redacted.

The deploy helper forces `ENABLE_PUBLIC_DEMO=1` for staging. Production defaults
to disabled and receives only the validated `enable_public_demo` workflow input;
application startup accepts the flag in staging or production and rejects it in
development. A disabled production promotion is the route kill switch. Before
cutover, the deploy helper verifies the active rollback marker and proves the
candidate's expected demo state. For an enabled candidate it performs the
CSRF-protected demo start, validates the secure session cookie, and resolves the
fictional dashboard while the existing release still serves traffic.

The former shared `owner@demo.local` staging tenant is retired at staging
startup because its published password allowed unrestricted writes. Development
seeding is rejected by staging configuration, and staging enrollment is closed
unless an operator supplies `SIGNUP_ALLOWLIST`. The staging smoke uses only
fresh, expiring demo tenants and proves their bounded ordinary form writes,
bounded `/import` CSV preview and commit, rejection of all other multipart
uploads, and isolation. Production smoke uses approved environment secrets for
a real pilot owner plus separate ephemeral demo sessions, performs only bounded
fictional writes, and proves the owner tenant remains separate. No shared demo
credentials are provisioned in either deployed environment.

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
| **Deploy Production** | Manual (`workflow_dispatch`) | Validate and promote a reviewed immutable digest → `:latest`, deploy by digest, smoke-test |

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
4. **Ship prod** — obtain the digest of the exact staging-reviewed commit image,
   then run **Deploy Production** with that digest. The workflow promotes it to
   `:latest` for discoverability but deploys the immutable digest. GitHub
   environment approval is required if configured.

Copy the exact commit SHA and immutable digest from the successful reviewed
**Deploy Staging** run's **Staging build artifact** summary:

```bash
gh workflow run deploy-production.yml \
  -f mode=promote \
  -f source_sha=<reviewed-40-character-commit> \
  -f image_digest=sha256:<reviewed-digest> \
  -f enable_public_demo=true
```

The production workflow accepts only a full lowercase 40-character source SHA
and `sha256:` followed by 64 lowercase hexadecimal characters. It pulls that
exact `ghcr.io/wmichelin/clubdue@…` reference and requires its immutable
`org.opencontainers.image.revision` label to match the supplied source SHA
before promotion. It never resolves a mutable `:staging` or commit tag as the
release source.

## Local deploy

```bash
cp .env.example .env   # fill in DROPLET_HOST + GITHUB_TOKEN
./deploy.sh staging    # safe, frequent
# ./deploy.sh production  # intentionally rejected; use the reviewed GitHub workflow
```

## Smoke tests

```bash
./scripts/smoke-test.sh staging
CLUBDUE_SMOKE_PUBLIC_DEMO=enabled ./scripts/smoke-test.sh production
```

Checks liveness/readiness, authenticated dashboard behavior, and environment
boundaries. Staging proves isolated one-click demo creation, bounded ordinary
form writes, bounded `/import` CSV preview and commit, rejection of other
multipart uploads, cross-visitor separation, and legacy-login retirement;
production proves the explicit demo state, isolated bounded writes, a second
visitor cannot see the first visitor's change, and the real owner session and
tenant remain separate.

See also [RELEASE-WORKFLOW.md](./RELEASE-WORKFLOW.md) for agent instructions.
