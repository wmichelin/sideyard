# Agent instructions

## Release workflow

ClubDue uses **staging → review → production**:

1. Push to `main` → staging auto-deploys to https://clubdue-staging.waltermichelin.com
2. Run `./scripts/smoke-test.sh staging` and verify the change
3. Show the user the staging URL and results; **wait for explicit approval**
4. Only then: `gh workflow run deploy-production.yml`

**Never deploy production without the user's OK in chat.**

See [docs/RELEASE-WORKFLOW.md](docs/RELEASE-WORKFLOW.md) for the full checklist.

## Stack

- Go single binary, embedded HTML/CSS templates
- Docker image ~11 MB (`FROM scratch`)
- Deploy scripts in `scripts/`, workflows in `.github/workflows/`

## Droplet isolation

Shared pantry DO droplet. Do not touch Pantry (`:8080`) or Headscale (`:8081`).
