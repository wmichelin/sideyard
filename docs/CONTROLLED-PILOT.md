# Controlled pilot guide

ClubDue is a recurring-service commitment-ledger pilot, not a billing, dispatch, or
customer-messaging system. This guide is an operating proposal; contractual,
privacy, retention, consent, and legal terms require human approval before a
real pilot begins.

## Pilot shape and decision

- **Duration:** 14 assisted days, with a day-7 check-in and a day-14 continue,
  pause, or exit decision.
- **Eligibility:** one trusted HVAC owner who is already invited and willing to
  work from a fixed CSV export. The operator provisions the approved owner;
  public production enrollment remains closed.
- **Supported work:** annual or monthly maintenance agreements represented by
  the current fixed CSV columns, deterministic renewal queue outcomes, and
  seasonal visit handoffs.
- **Out of scope:** card collection, automatic email/SMS, appointment booking,
  Jobber API write-back, dispatch, mobile technician workflow, and multi-user
  roles. A Jobber handoff records a click/time, not an appointment.
- **Success signal:** the owner imports a reconciled book, completes a first
  renewal or visit action, returns on a later day, and reports whether manual
  handoff stays below the agreed weekly double-entry limit.

The original HVAC pilot remains supported. A landscaping design-partner track
uses the same tenant/customer/property/agreement core plus recurring service
schedules. Its research thesis, interview questions, four-week usage gates,
and explicit product non-goals are in
[PRODUCT-MARKET-FIT-PATH.md](PRODUCT-MARKET-FIT-PATH.md). A public fictional
demo is not a real-data pilot and is never an approved place for customer data.

## Assisted onboarding and first run

1. Confirm the owner invitation in the support log, then use the audited
   [owner-provisioning procedure](OWNER-PROVISIONING.md). Deliver credentials
   out of band; never place them in a ticket, CSV, shell history, or source.
2. Work from the fictional [concierge fixture](../internal/app/testdata/pilot-concierge-import.csv).
   Copy it locally; replace every row with approved customer data only after
   the owner validates the required fixed columns and `external_id` source.
3. In **Import**, upload and preview the file. Resolve every displayed error
   before commit. The preview is the reconciliation checkpoint, not a
   background import.
4. Commit, open **Agreements**, and compare customer/agreement counts and a
   handful of source rows. Re-importing a corrected row with the same
   `external_id` updates it rather than making a duplicate.
5. Open **Renewals** and **Visits**, record one real owner action only when the
   owner is ready, then use **Export CSV** to hand off work to Jobber manually.

The self-cleaning `./scripts/pilot-onboarding-rehearsal.sh` proves this
fictional flow from a fresh shop through preview, commit, correction/re-import,
agreement visibility, and CSV export. It records elapsed first-value time and
fails if it exceeds 15 minutes. It never contacts staging or production.

## Privacy, data, support, and feedback

- Store only data needed for the agreement workflow. The activation table
  records a shop ID, fixed milestone name, and timestamp—never customer data,
  owner email, IP address, device identifier, or free text.
- Keep exported CSVs and backups restricted to the approved operator/owner.
  Use the encrypted backup and isolated restore procedures in
  [DEPLOY.md](DEPLOY.md). Process access, correction, and deletion requests in
  the support log; execute deletion only with authorized human review and
  preserve only records required by approved policy.
- Capture support requests, import exceptions, correction source, and manual
  workaround in the support log, not in activation events. Escalate security,
  mistaken disclosure, data-loss, or access-recovery concerns to the pilot
  owner immediately and pause affected work until resolved.
- Collect feedback at day 1, day 7, and day 14: time to first value, rows
  reconciled, renewal/visit actions, export handoffs, duplicate corrections,
  manual minutes, workflow failures, and requested changes. Do not place
  homeowner details in feedback notes.

## Export, correction, exit, and rollback

Before each Jobber paste, download the current visit CSV, label the handoff in
the support log, and reconcile Jobber completion manually. To correct data,
fix the source CSV, preview it, commit it with the same `external_id`, then
check the agreement and export again. Do not treat the CSV as an automatic sync.

At exit, stop new imports/actions, export the owner-approved records, follow
the approved deletion/retention decision, and remove access using the owner
recovery/provisioning procedure. For application rollback, follow the verified
staging fix-forward/revert path in [OPERATIONS-RUNBOOK.md](OPERATIONS-RUNBOOK.md).
Production changes always require explicit chat approval and GitHub environment
approval; this guide grants neither.

## Honest FAQ

**Does ClubDue charge cards or mark payments paid?** No. Payment remains a
manual owner/shop conversation.

**Does it create Jobber appointments?** No. It exports CSV and records a
handoff only.

**Does activation telemetry identify homeowners?** No. It captures one
first-occurrence milestone per shop for pilot learning.

**Can an operator accept every spreadsheet?** No. The present importer accepts
the documented required columns plus optional contact/plan fields, common
Google Sheets CSV formatting, and a stable external ID. It does not infer
arbitrary headings or connect directly to Google. Resolve mappings and
exceptions manually before commit, and do not claim fit for the landscaping
partner until an authorized sanitized sheet reconciles successfully.
