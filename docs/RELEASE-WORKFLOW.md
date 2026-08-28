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

**Do not deploy to production until the user explicitly approves.**

### 4. Production (after user OK)

When the user says OK / ship it / looks good:

```bash
gh workflow run deploy-production.yml
```

This promotes the `:staging` image (what they just reviewed) to `:latest` and deploys production.

If the user approved a specific commit rather than current staging:

```bash
gh workflow run deploy-production.yml -f promote_tag=<git-sha>
```

Monitor the run:

```bash
gh run watch
```

Confirm with `./scripts/smoke-test.sh production`.

### 5. If staging is broken

Fix forward on `main` — do not deploy production. Staging should recover on the next green **Deploy Staging** run.

## Rules

- **Never** skip staging for user-visible changes.
- **Never** trigger **Deploy Production** without explicit user approval in chat.
- **Never** modify Pantry, Headscale, or their nginx configs on the droplet.
- Prefer small, frequent merges to `main` over large batches.
