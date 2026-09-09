# Agent instructions

## Product documentation

[docs/PRODUCT-MAP.md](docs/PRODUCT-MAP.md) is the canonical product documentation
for Sideyard and the starting point for client onboarding and product awareness.
Read it before planning or changing product behavior or setting up a new client.

- Update its directed graph and page catalog in the same change as additions,
  removals or changes to pages, navigation, features, permissions, configuration,
  integrations or user-visible limits. Every graph node must document the page's
  features, route, access requirements and release availability, with source links.
- Keep the client-onboarding paths and trade-specific capabilities/gaps current.
  Document form actions within their page; account for new routes with a node or
  an explicit exclusion. Preserve stable node IDs and check links and graph syntax.
- Distinguish verified production behavior, staging-only features, conditional
  capabilities and future work. Update release labels after verified deployment;
  do not present roadmap items or marketing claims as implemented features.
- Keep this map as the product source of truth; link to it from other documents
  instead of creating competing feature catalogs. Release evidence belongs in
  `docs/SIDEYARD-DELIVERY-STATUS.md`; future scope belongs in the execution plan.

## Release workflow

ClubDue uses **staging → review → production**:

1. Push to `main` → staging auto-deploys to https://clubdue-staging.michelindigitalmedia.com
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

## Issue delivery

For a GitHub issue, invoke `github-issue-intake` first; it posts a delivery
brief and hands a ready issue to `team-delivery-workflow`. Invoke
`team-delivery-workflow` directly for a manual request, delivery brief,
approved conformance plan, or delivery-ready issue; intake is not required for
those paths.

## Engineering philosophy

Read and follow [ENGINEERING_PHILOSOPHY.md](ENGINEERING_PHILOSOPHY.md).
