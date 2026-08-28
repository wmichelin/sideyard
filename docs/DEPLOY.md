# Deployment

ClubDue runs on the **same DigitalOcean droplet as Pantry**, in isolated containers:

| Env | URL | Container | Port | Image tag |
|-----|-----|-----------|------|-----------|
| **Staging** | https://clubdue-staging.michelindigitalmedia.com | `clubdue-staging` | `127.0.0.1:8082` | `:staging` |
| **Production** | https://clubdue.michelindigitalmedia.com | `clubdue` | `127.0.0.1:8083` | `:latest` |

Pantry (`:8080`) and Headscale (`127.0.0.1:8081`) are untouched.

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
