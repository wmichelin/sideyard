# Sideyard signup notifications and identity correction

## Delivery brief and review

Replace the rejected S favicon, provide a dedicated social preview image, and
notify the owner when a visitor registers interest. Preserve the landing form,
existing signups, both production hostnames, and staging isolation.

- Product: clear, recognizable small icon; shared links show a designed card.
  Owner receives signup details at `wmichelin@gmail.com` through Resend.
- Architecture: persist notifications alongside new signups in one transaction.
  Send from a durable queue with retries; do not lose a signup if mail is down.
  Suppress duplicate-email submissions and serialize concurrent delivery workers.
- Testing: prove atomic queue creation, duplicate suppression, failed-send retry,
  successful delivery acknowledgement, and recipient isolation. Inspect actual
  image assets and browser metadata at mobile and desktop sizes.
- Engineering: keep images native to SVG with PNG exports for clients that
  require raster previews. Inject the email transport; never log credentials or
  signup contact details. No unrelated app redesign.
- DevOps: connect the service through private server configuration, use TLS, and keep staging mail
  separate. Stage and verify before the already-authorized production release.
- QA: verify a signup reaches the owner's inbox, not just an HTTP success page.
  Verify favicon, share image, old hostname, existing demo, and login routes.

Staging: https://clubdue-staging.michelindigitalmedia.com (verified).
Production: https://getsideyard.com (assets, signup queue, and inbox delivery verified).

## Email configuration and operation

The prepared transport uses [Resend's send-email API](https://resend.com/docs/api-reference/emails/send-email).
Configure `RESEND_API_KEY`, `INTEREST_NOTIFY_FROM`, and `INTEREST_NOTIFY_TO`
together in `/opt/clubdue/production.env` (mode 600), then redeploy through the
production workflow. The deploy script passes them by environment-variable name
so keys are not placed in command arguments. Use a sender verified in the email
service and the owner's chosen destination. Never copy production mail settings
into staging; optional staging delivery gets a `[STAGING]` subject prefix.

New signups and their notification rows are committed atomically. Duplicate
email submissions do not create additional alerts. The worker polls every 15
seconds, retries failed sends with delays from one minute up to one hour, and
retains undelivered alerts while the service is unconfigured. It sends plain text
only to the configured owner, with the visitor's name, email, business, trade,
and message. Provider acknowledgement is recorded in `interest_notifications`.
Existing signups are preserved and are not automatically re-mailed.

**Azure preparation update — pending staging verification:** the new worker uses
a buffered wake after a committed signup and sleeps without a timer when no work
is pending. It checks queued work at startup, retries known pending work at its
stored deadline (with a 15-second minimum for contention/batch continuation), and
backs off database failures from one minute up to one hour. Daily reconciliation
shares the existing maintenance window rather than waking an idle database on a
separate schedule. Wakes can interrupt a later retry deadline.

The worker remains alive through graceful HTTP shutdown so in-flight signups can
still wake it. Current deployments retain the previous app container running;
its pending work stays recoverable and row locks prevent simultaneous claims.
An interrupted process handoff can still require startup/daily reconciliation,
potentially delaying an alert up to a day if no earlier wake occurs. This remains
at-least-once delivery with the provider's bounded duplicate suppression below.
Production still uses the previously verified polling release until promotion.

Native SQL Server schema and queries for interest/outbox pass the same retry and
concurrent-worker tests as PostgreSQL, plus atomic concurrent signup and Unicode
boundary checks. The application's SQL Server selector remains disabled; this is
one ported domain, not a completed database migration.

Workers coordinate with Postgres row locks across rolling deployments. Stable
[idempotency keys](https://resend.com/docs/dashboard/emails/idempotency-keys)
protect retries within Resend's 24-hour deduplication window. Delivery is at
least once: a crash after provider acceptance followed by a retry more than
24 hours later can still produce a duplicate. Success means provider acceptance;
inbox receipt must be checked separately during service activation.

The new table is additive and supports rollback to the prior application image.
Queue rows are deleted with their signup, including synthetic verification data.
The runtime image includes CA certificates for verified HTTPS delivery.

## Image sources

The native icon is `internal/app/static/sideyard-mark.svg`. The share-card source
is `scripts/assets/sideyard-share.svg`, rendered as a 1200×630 PNG; icon PNG
exports are 32×32 and 180×180. Landing metadata explicitly names the share card,
its size, format, and alt text. All full-page templates share favicon metadata.
Versioned URLs give clients fresh asset addresses; messaging apps may retain
previews already cached in existing conversations.

## Verification

- Current source `7d33ab7ff1c05a796bd351b647f81eea58729726`; immutable image
  `sha256:f54daa3e44102219717683ee877f71117d7555361e3829495ffedcfcecdb1cd2`.
- Local `go vet ./...` and `go test -count=1 ./...` passed with mandatory
  Postgres integration on a fresh disposable database. Docker build passed.
  Fixed an existing interest-test cleanup ordering error uncovered by queue
  tests; the database now stays open until its synthetic signup is deleted.
- CI `34230433745`, staging `34230433487`, and production `34231076171` passed.
  Manual staging smoke passed. Production owner login and isolated demo smoke
  passed, and the old hostname's browser demo still reaches its own dashboard.
- Browser checks on local, staging, and production verified mobile/desktop
  layout, form validation, submission, and confirmation without browser errors.
  Image decoding and metadata checks verified 1200×630 share PNG, 32px favicon,
  180px Apple icon, and the versioned SVG on landing, login, and thanks pages.
- Each live synthetic signup created exactly one pending notification. Test
  signups and their queued alerts were removed after verification. No production
  5xx, panic, or signup-persistence errors appeared. Production gate reset false.

## Email activation — September 8, 2026

- Production sends from `Sideyard <signups@updates.getsideyard.com>` to
  `wmichelin@gmail.com`. The sending-only key is stored privately in the mode-600
  production environment file. Staging has no production email configuration.
- Resend domain `updates.getsideyard.com`, ID
  `6d67e2d6-504a-41b2-b045-c19381e04d94`, is verified. Namecheap has the generated
  TXT record at `resend._domainkey.updates`, CNAME `rsend.updates` pointing to
  `rsend.forge.rmta.net`, and CNAME `send.updates` pointing to
  `send.forge.rmta.net`, all with five-minute TTLs. Existing website and mail
  forwarding records were preserved; receiving remains disabled in Resend.
- The production worker accepted a live form's synthetic signup on its first
  attempt at `2026-09-08 13:24:42 UTC`, provider message ID
  `dbfd1ded-662c-4e1d-8fcd-6d57ca79ebd3`. Gmail confirmed the matching
  `New Sideyard signup` email at `13:24:43 UTC`, message ID
  `1a0813141ff3d5ba`, with INBOX and CATEGORY_PROMOTIONS labels. This verifies
  the actual form, durable queue, production worker, branded sender, and inbox.
- Browser checks at 375, 390, 768, and 1440 pixels passed required-field
  validation, submission, and confirmation without browser errors. The synthetic
  signup and its notification row were removed afterward; test emails remain
  unread in the owner's inbox.
- The temporary full-access domain-verification key was revoked after setup.
  The production sending-only key remains active. Production deployment gate is
  reset to false.
