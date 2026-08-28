# ClubDue

Track membership dues for your club — who owes what, who's paid, and when.

Single Go binary with embedded HTML/CSS (~10 MB Docker image, ~10–20 MB RAM at runtime).

## Run locally

```bash
go run .
# http://localhost:8080
```

Or with Docker:

```bash
docker build -t clubdue .
docker run --rm -p 8080:8080 clubdue
```

## Deploy

Staging and production on the pantry DO droplet (Pantry/Headscale untouched):

| Env | URL |
|-----|-----|
| Staging | https://clubdue-staging.waltermichelin.com |
| Production | https://clubdue.waltermichelin.com |

Push to `main` → staging auto-deploy. Production is manual after review.

See [docs/DEPLOY.md](docs/DEPLOY.md) and [docs/RELEASE-WORKFLOW.md](docs/RELEASE-WORKFLOW.md).

## Features (planned)

- Member roster with contact info
- Dues periods (monthly, quarterly, annual)
- Payment tracking and balance summaries
- Export for treasurers
