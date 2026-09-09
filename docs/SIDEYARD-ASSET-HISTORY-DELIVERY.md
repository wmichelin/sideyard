# Vehicle and equipment service histories

Status: verified
Last completed: CI, staging smoke, browser acceptance and running artifact verification passed.
Now: durable handoff of vehicle and equipment histories.
Next: customer/visit-linked notes and remaining E2 timeline work, then E3 scheduling.
Staging: https://clubdue-staging.michelindigitalmedia.com (verified)
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

## Staging validation and slow-script correction

- Initial source `d33983fe0ed1de3620979fc50686f35cd2f87736`: CI
  `34314565779` and Deploy Staging `34314565730` passed. Manual smoke passed.
- The first browser attempt read defaults before the deferred script loaded.
  Waiting for DOMContentLoaded made the complete staging flow pass. Investigation
  also found a real slow-script edge case: defaults could overwrite values entered
  before the script arrived. A deliberately held script reproduced that failure
  against the original binary; guarding changed fields fixed it.
- Static rebuild and the complete local browser flow, including deliberately
  delayed JavaScript, passed with the correction.

## Final staging evidence

- Source: `ea9d91d9f6892a8b1a7d036429081a819eed3e82`.
- Image: `ghcr.io/wmichelin/clubdue@sha256:6567c243466dc954ece39c91d5a2210c73879b63fee9ec359b2c80d92a751e32`.
- [CI](https://github.com/wmichelin/clubdue/actions/runs/34314814217) and
  [Deploy Staging](https://github.com/wmichelin/clubdue/actions/runs/34314814203)
  succeeded. Running image/revision read directly from the staging container.
- `./scripts/smoke-test.sh staging`: passed.
- `node /tmp/sideyard-qa/history.cjs https://clubdue-staging.michelindigitalmedia.com staging`:
  passed separate vehicle/equipment histories, date-only/timed notes, DST
  validation/selection, corrections, delayed defaults and no-JavaScript fallback
  at 390/1440 pixels. No browser errors or horizontal document overflow; final
  mobile screenshot reviewed. Only isolated fictional demo records were used.
- Recent staging logs showed no unexpected errors. Production remains on
  `7d33ab7ff1c05a796bd351b647f81eea58729726`; its gate remains `false`.
- Local QA servers and disposable database removed. No production deployment.
