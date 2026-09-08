# Sideyard launch

## Delivery brief

Publish the existing app as Sideyard at `https://getsideyard.com`, with a public
interest page inspired by Michelin Digital Media's hero, benefits, and contact
flow. Redirect `sideyard.work` and the new domains' `www` aliases to the primary
address. Keep `clubdue.michelindigitalmedia.com` serving the app directly alongside
the new primary host. Preserve customer accounts, app routes, data, and isolated demos.
The user explicitly authorized deployment and Namecheap DNS changes in chat.

Staging: https://clubdue-staging.michelindigitalmedia.com (pending).

## Review before implementation

- Product: use plain contractor language and several trade examples. Acceptance:
  working mobile page, useful interest form, durable submission, confirmation,
  existing sign-in/demo access, and HTTPS on the purchased domains. Do not invent
  pricing, testimonials, customer counts, or features.
- Architecture: reuse the Go service and Postgres. Add a separate interest table
  with normalized unique email and bounded fields; duplicate submissions do not
  overwrite existing information. Keep inquiries separate from tenant accounts.
  The additive migration remains compatible with the previous binary.
- Testing: cover valid/invalid input, duplicate persistence, database failure,
  CSRF, body limits, honeypot, throttling, and private operator CSV export.
- Engineering: keep the existing binary/module/container names for operational
  compatibility; rebrand visible strings. Use a standalone landing template and
  stylesheet. Inject the inquiry persistence dependency for failure tests.
- DevOps: stage and run existing checks before promoting the exact image digest
  through the production workflow. Provision only Sideyard/ClubDue vhosts;
  preserve all unrelated DNS records and Pantry/Headscale services. DNS credentials
  use the user's authenticated Namecheap browser session; server SSH works.
- QA: exercise desktop/mobile, keyboard form submission, validation, success,
  persisted record, redirects, TLS, existing demo, and login access on staging
  and production. Remove only clearly identified synthetic verification leads.

Decision: implement. Production publication is explicitly authorized by the
current user request; staging verification remains mandatory. No outreach or
automatic subscriber emails are part of this change.

## Operator access to interest submissions

`docker exec clubdue-current /clubdue export-interest` writes a CSV to stdout.
Store exports privately; they contain contact details. This operator command is
not exposed as a public or tenant-accessible web route. Submissions live in the
`interest_signups` table and are included in existing Postgres backups.

## Domain configuration

Namecheap: replace only the parking CNAME for `www` and URL redirect for `@`
on each purchased domain with A records to `45.55.214.46`, TTL five minutes.
Preserve email forwarding and its SPF record. Before this change both domains
had `www` CNAME `parkingpage.namecheap.com.` and an unmasked `@` redirect to
their own `http://www.<domain>/`.

The nginx template is `scripts/nginx/sideyard.conf`. Install it as
`/etc/nginx/sites-available/getsideyard.com`, substituting the current
`clubdue-current` loopback port (8083 or 28083). Both app hostnames share the
single upstream so promotion and rollback switch them together. Only after
the new SAN certificate is ready, replace the old ClubDue enabled symlink with
the new one. Back up both ClubDue files first; validate with `nginx -t` before
every reload. Do not change any unrelated vhost.

Certificate: Certbot webroot `/var/www/sideyard-acme`, certificate name
`getsideyard.com`, covering the primary, old ClubDue hostname, and three redirect
aliases listed in the template. Keep the HTTP challenge location for renewals
and use a deploy hook to reload nginx after successful renewal.

## Verification

Local: `go vet ./...`, `go test -count=1 ./...` with required Postgres integration,
and `docker build -t sideyard-launch:local .` passed using the official Go 1.23
image. Playwright checked 375, 390, 768, and 1440 pixel widths, required fields,
form submission, confirmation, and absence of browser errors; the synthetic
submission was verified in Postgres.

## Remaining deployment work

Staging review, Namecheap records, certificates, immutable
production promotion, and end-to-end production verification.
