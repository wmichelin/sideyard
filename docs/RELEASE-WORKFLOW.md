# Release workflow (agents)

Follow this when making ClubDue changes that should reach users.

## Environments

- **Staging** — https://clubdue-staging.michelindigitalmedia.com — auto-deploys on every push to `main`. Experiment freely here.
- **Production** — https://clubdue.michelindigitalmedia.com — deploy only after the user approves staging.

## Agent checklist

### 1. Ship to staging (automatic)

After pushing to `main`:

- Wait for **Deploy Staging** workflow to finish (`gh run list --workflow=deploy-staging.yml --limit 1`).
- If it failed, fix and push again.

### 2. Verify staging yourself

```bash
./scripts/smoke-test.sh staging
```

Also open https://clubdue-staging.michelindigitalmedia.com and confirm the change visually when UI-related.

### 3. Show the user

Report:

- What changed (brief)
- Staging URL: https://clubdue-staging.michelindigitalmedia.com
- CI / Deploy Staging run status (link if available)
- Smoke test result
- Exact staging commit SHA and immutable image digest to use if approval follows

**Do not deploy to production until the user explicitly approves.**

### 4. Production (after user OK)

When the user says OK / ship it / looks good:

1. Identify the exact successful **Deploy Staging** run the user reviewed. Copy
   the full commit SHA and immutable image digest from that run's **Staging
   build artifact** summary. Do not substitute the current `main` SHA or
   reconstruct the release digest from a mutable tag if a newer build or rerun
   exists.
2. Confirm the recorded source is a full 40-character lowercase commit SHA and
   the digest is `sha256:` followed by 64 lowercase hexadecimal characters.
   The staging build stores that SHA in the image's
   `org.opencontainers.image.revision` label. Optionally prove the immutable
   reference resolves before promotion:

   ```bash
   docker pull "ghcr.io/wmichelin/clubdue@sha256:<reviewed-digest>"
   ```

3. Enable the production gate immediately before dispatching the reviewed
   artifact, then close it when the promotion finishes:

```bash
gh variable set PRODUCTION_DEPLOY_ENABLED --body true
gh workflow run deploy-production.yml \
  -f mode=promote \
  -f source_sha=<reviewed-40-character-commit> \
  -f image_digest=sha256:<reviewed-digest> \
  -f enable_public_demo=true
```

The workflow validates the source and digest, pulls the exact image, requires
its immutable revision label to match the reviewed source SHA, promotes it to
`:latest`, and deploys the droplet by the immutable digest with the explicitly
approved demo setting. Set
`PRODUCTION_DEPLOY_ENABLED` back to `false` when the approved promotion
finishes. Its run summary records the reviewed image reference; include that
digest with the release evidence. Autonomous roadmap and issue agents must
never enable it.

Monitor the run:

```bash
gh run watch
```

The workflow runs the production smoke with the expected demo setting. For a
manual repeat, provide the approved owner credentials without printing them:

```bash
CLUBDUE_SMOKE_PUBLIC_DEMO=enabled \
  CLUBDUE_SMOKE_EMAIL='<approved owner>' \
  CLUBDUE_SMOKE_PASSWORD='<approved secret>' \
  ./scripts/smoke-test.sh production
```

If the promoted app release must be reversed, use the same approved production
window and run the GitHub workflow only (never deploy directly from a laptop):

```bash
gh workflow run deploy-production.yml -f mode=rollback
```

Rollback restores the retained previous application container; it does not roll
back database schema or data. Use it only when the retained binary remains
compatible with the database migrations already applied.

Then close the promotion window:

```bash
gh variable set PRODUCTION_DEPLOY_ENABLED --body false
```

### 5. If staging is broken

Fix forward on `main` — do not deploy production. Staging should recover on the next green **Deploy Staging** run.

## Rules

- **Never** skip staging for user-visible changes.
- **Never** trigger **Deploy Production** without explicit user approval in chat.
- **Never** use `./deploy.sh production` or `./deploy.sh prod`; those local
  paths fail closed before loading secrets, building, or connecting to a host.
- **Never** modify Pantry, Headscale, or their nginx configs on the droplet.
- Prefer small, frequent merges to `main` over large batches.
