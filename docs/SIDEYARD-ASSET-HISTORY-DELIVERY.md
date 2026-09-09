# Vehicle and equipment service histories

Status: testing
Last completed: implementation, automated checks and local browser acceptance passed.
Now: publishing independent authored timelines for vehicles and equipment.
Next: CI, staging smoke and staging browser flows.
Staging: https://clubdue-staging.michelindigitalmedia.com (pending for this slice)
Blocker: none. Production requires separate approval.

## Delivery brief and team review

- Product manager: owners can open history for each vehicle or equipment item and
  add internal service notes without overwriting the current standing details.
  Event date is required; time is optional so an unknown time stays unknown.
  Corrections are separate entries. Customer-facing pages/exports exclude these
  notes. Actual service visits and property-note time upgrades remain next slices.
- Architect: additive asset_notes table with exactly one vehicle/equipment parent
  and composite tenant foreign keys. Keep event date, optional absolute timestamp,
  IANA timezone, author and recording timestamp separate. Never move existing
  asset identities. Reuse immutable-entry/idempotent-submission conventions.
- Unit-testing expert: independent histories, foreign tenants and staff denial,
  preserved corrections/authorship, concurrent retries and changed-content reuse;
  date-only entries, timezone validation, DST gaps and repeated times. A repeated
  local time must require an explicit offset choice, not silently guess.
- Staff software engineer: a shared asset-history page with two explicit parent
  routes. Keep user input on errors. Optional small JS sets the initial date/zone
  from the browser; the form still works without JS using its visible UTC default.
  Embed tzdata so the scratch image can interpret IANA zones reliably.
- DevOps engineer: no new service or secret. Additive migration and embedded
  timezone data; run full Go/Postgres checks, static build, both rehearsals and
  staging smoke/browser checks. Database backup includes all new note history.
- QA tester: two vehicles and an equipment item retain independent histories;
  add date-only and timed notes, reject a DST gap, disambiguate a repeated time,
  preserve a correction and inspect 390/1440-pixel views in an isolated demo.

Decision: proceed with owner-only asset histories. Timezone defaults are editable
per entry rather than guessed from the property address. Event times do not imply
work duration, scheduled appointment times or billing completion. Display latest
100 entries by event date then recording order; retain all earlier rows. Expanded
timeline pagination, asset photos and crew permissions remain later work.

## Local verification

- Go 1.23 container: `go vet ./...`, `go test -count=1 ./...` with
  `CLUBDUE_REQUIRE_POSTGRES_INTEGRATION=1` and a disposable Postgres 16 database,
  then `CGO_ENABLED=0 go build -o /qa/sideyard-history-qa ./cmd/clubdue`: passed.
- Coverage includes tenant/role isolation, separate parent histories, concurrent
  retries, changed-submission conflicts, immutable corrections/authorship and
  database rejection of foreign or multiple parents.
- Event-time cases cover date-only precision, timezone date boundaries, future
  events, invalid input, DST gaps/folds, half-hour transitions and a skipped day.
- `./scripts/production-rehearsal.sh` and
  `./scripts/pilot-onboarding-rehearsal.sh`: passed. The production rehearsal
  saves an ambiguous-clock event with an explicit offset inside the scratch
  container and verifies its exact timestamp, timezone, body and author after
  encrypted backup restoration.
- `node /tmp/sideyard-qa/history.cjs`: passed at 390/1440 pixels. Two vehicles
  and equipment retain separate histories; date-only/timed notes, DST error
  recovery, explicit repeated-clock selection, corrections, escaped text and
  no-JavaScript fallback passed. No browser errors or document overflow. Mobile
  screenshot inspected; scripts/screenshots retained under `/tmp/sideyard-qa/`.
- Migration 028 is additive. Rolling back the application can leave the table
  and its history intact; it does not require removing stored notes.
