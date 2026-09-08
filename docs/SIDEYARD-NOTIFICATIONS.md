# Sideyard signup notifications and identity correction

## Delivery brief and review

Replace the rejected S favicon, provide a dedicated social preview image, and
notify the owner when a visitor registers interest. Preserve the landing form,
existing signups, both production hostnames, and staging isolation.

- Product: clear, recognizable small icon; shared links show a designed card.
  Owner receives signup details without exporting a CSV. Confirm destination
  and available sending service through the pending chat questions.
- Architecture: persist notifications alongside new signups in one transaction.
  Send from a durable queue with retries; do not lose a signup if mail is down.
  Suppress duplicate-email submissions and serialize concurrent delivery workers.
- Testing: prove atomic queue creation, duplicate suppression, failed-send retry,
  successful delivery acknowledgement, and recipient isolation. Inspect actual
  image assets and browser metadata at mobile and desktop sizes.
- Engineering: keep images native to SVG with PNG exports for clients that
  require raster previews. Inject the email transport; never log credentials or
  signup contact details. No unrelated app redesign.
- DevOps: credentials are absent from Sideyard's environment. Connect the chosen
  service through private server configuration, use TLS, and keep staging mail
  separate. Stage and verify before the already-authorized production release.
- QA: verify a signup reaches the owner's inbox, not just an HTTP success page.
  Verify favicon, share image, old hostname, existing demo, and login routes.

Staging: https://clubdue-staging.michelindigitalmedia.com (pending).

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
