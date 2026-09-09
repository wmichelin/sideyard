# Standalone jobs and site surveys

Status: testing
Last completed: implementation, full local checks and browser acceptance passed.
Now: publishing standalone work and the reported status-badge layout fix.
Next: CI, staging smoke and browser verification.
Staging: https://clubdue-staging.michelindigitalmedia.com (pending for this slice)
Blocker: none. This new slice will be staged for review.

## Production promotion

User explicitly requested “push to production and then continue to the next feature.”
Promoted source `ae6f16ae9f9feaf52bf58093cf1f0c3b8c7a7c34`, image
`sha256:a57a268f081b8f2c96550eb2d61b00e36faae855757662c0a62b27162f5d319c`,
from reviewed staging run `34353916405`. Production run `34354641834` succeeded,
including its authenticated smoke test. Running image/revision matched; production
visit-history browser acceptance passed at 390/1440 pixels. Gate reset to false.

## Delivery brief and six-perspective review

- Product manager: create a one-time job or estimating/site-survey visit for a
  customer, optionally choose their property, leave the date unplanned or assign
  a day, and record completion/cancellation. No annual plan prerequisite. Show
  jobs in a dedicated owner Jobs view; appointments/crew and calendar integration
  are subsequent work. Fix wrapped “Due today” badges from the user's screenshot.
- Architect: new jobs domain and tenant-safe storage with immutable customer
  identity, optional same-customer property, optimistic revisions and authored
  change snapshots. Existing recurring/seasonal work stays authoritative and is
  not copied into jobs. Later bridging must reference those existing records.
- Unit-testing expert: foreign tenant/customer/property rejection, concurrent
  create retries, changed-key conflicts, stale edits/outcomes, completion once,
  reopening history, independent jobs and absence of new agreements/entitlements.
- Staff software engineer: explicit create/edit/outcome methods; one authoritative
  job state and transactional history. Completed/cancelled jobs require explicit
  reopening before edits. Keep date-only scheduling honest; no invented start
  time or invoice. Use current app adapters over a cohesive internal/jobs package.
- DevOps engineer: additive migration, no new services/secrets. Old binaries can
  ignore jobs, but rolling back hides their work queue; retain data and disclose
  that operational limitation. Run full checks, backup restore and staged UI QA.
- QA tester: create an unplanned survey and a dated service job without an
  agreement; move one, reject a stale save, complete/reopen and inspect history.
  Verify property scope, tenant/role denial and phone/desktop views. Reproduce
  status wrapping at intermediate widths and verify single-line badges without
  mobile overflow.

Decision: implement the bounded E3.1 foundation. Quotes/invoices, crew assignment,
appointment times, treatment programs, existing-visit bridges and job photo/asset
associations remain later slices. Customer/property timeline finish remains open.

## Local verification

- `go vet ./...`, `go test -count=1 ./...` with required Postgres integration and
  `CGO_ENABLED=0 go build -o /qa/sideyard-jobs-qa ./cmd/clubdue`: passed using Go
  1.23 and disposable Postgres 16. Unit cases cover invalid fields, optional date
  and valid survey/job input. Integration covers concurrent creation, changed
  retries, same-customer property constraints, stale edits/outcomes, completed
  timestamps, reopening/cancellation, authored history and tenant/role isolation.
- Jobs create no agreements, seasonal visits or recurring occurrences. Customer
  deletion cannot erase jobs; whole-shop/demo cleanup cascades successfully.
- `./scripts/production-rehearsal.sh` and
  `./scripts/pilot-onboarding-rehearsal.sh`: passed. Production rehearsal creates
  a survey through HTTP, retries creation, completes it and verifies its state,
  unknown planned date, private details and both authored events after restore.
- `node /tmp/sideyard-qa/jobs.cjs`: passed at 390/1440 pixels. Created an
  unplanned survey, changed type/date, protected an old tab, completed/reopened,
  cleared date and retained five history entries. A second cancelled job stayed
  separate; open/all filters worked; no agreement was generated. No browser
  errors or overflow; mobile screenshot reviewed.
- `node /tmp/sideyard-qa/status-badges.cjs https://clubdue-staging.michelindigitalmedia.com override`:
  changed CSS passed 90 badges at 320/390/900/1024/1200/1440 pixels with no wrapping
  or document overflow. Mobile/desktop row screenshots inspected. The same check
  against unchanged CSS reproduced wrapped statuses. One initial browser launch
  encountered `ERR_NETWORK_CHANGED` during Docker network setup; rerun passed.
- The badge change is scoped to service-work status labels and their mobile grid.
  Label text and business state are unchanged.
