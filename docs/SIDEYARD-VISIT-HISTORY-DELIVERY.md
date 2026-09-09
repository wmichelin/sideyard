# Visit-linked internal notes

Status: verified
Last completed: CI, staging smoke, both browser flows and running artifact verification passed.
Now: durable handoff of visit-linked notes.
Next: remaining customer/property timeline work, then standalone jobs and scheduling.
Staging: https://clubdue-staging.michelindigitalmedia.com (verified)
Blocker: none for development. Production scope clarification is pending.

The user's “Do it” followed a message containing both a verified staging release
and further development steps. Asked which action they meant and continued under
the standing development authorization. No production dispatch or gate change.
If they approve the release already shown before this slice, its reviewed source
is `ea9d91d9f6892a8b1a7d036429081a819eed3e82`, image
`sha256:6567c243466dc954ece39c91d5a2210c73879b63fee9ec359b2c80d92a751e32`.
Do not silently substitute this newer visit-note build for that earlier review.

## Delivery brief and team review

- Product manager: owners can record private observations before or after a
  recurring/seasonal visit, with optional event time and preserved corrections.
  Adding a note must not mark work complete, consume entitlements or generate a
  charge. Link each visit from Service work; keep notes out of existing exports.
- Architect: additive visit_notes table with exactly one tenant-safe recurring
  occurrence or seasonal visit parent. Preserve the existing authoritative work
  IDs and states. Share event-time validation and note rendering with asset
  histories without merging their tables or changing existing stored history.
- Unit-testing expert: cross-tenant/role denial, independent visits, retry and
  correction preservation, completed-visit notes, no completion/entitlement
  changes, export exclusion, and history surviving rescheduling/completion.
- Staff software engineer: two explicit routes and one visit-history handler;
  shared input/event entry types and form template. Do not copy a second clock
  parser or expose storage identifiers in ordinary page text.
- DevOps engineer: additive migration with no service/secrets needed; existing
  backup captures note rows. Verify migration/static build, both rehearsals and
  real staging workflows. Previous binary can ignore the new table on rollback.
- QA tester: add distinct notes to recurring and seasonal visits in a fictional
  demo; preserve corrections and optional time, then check phone/desktop layouts,
  retained work state and existing CSV behavior. New links remain owner-only.

Decision: implement the bounded visit-note slice. Standalone jobs, assignment,
appointment windows, billing triggers, customer timelines, property timestamp
upgrades and job/asset photos remain separate steps in the execution plan.

## Local evidence

- `go vet ./...` and `go test -count=1 ./...` in Go 1.23 with a disposable
  Postgres 16 database and required integration tests: passed. Static build
  `CGO_ENABLED=0 go build -o /qa/sideyard-visit-history-qa ./cmd/clubdue`: passed.
- Focused visit/asset history tests passed tenant/owner isolation, concurrent
  retries, preserved authorship/corrections, date-only precision, DST selection,
  independent visits, unchanged work counts/status and CSV exclusion. History
  survives rescheduling/completion; post-completion notes do not consume visits.
- The initial test compile referenced an unavailable comparison dependency.
  Used the repository's standard-library comparison approach instead; no new
  dependency was introduced. Focused and full runs then passed.
- `./scripts/production-rehearsal.sh` and
  `./scripts/pilot-onboarding-rehearsal.sh`: passed. Production rehearsal saves
  and retries a private visit note through HTTP, then proves exact timestamp,
  timezone, author and body restore with visit status and entitlement unchanged.
- `node /tmp/sideyard-qa/visit-history.cjs`: passed independent recurring/seasonal
  histories, corrections, optional event times, rescheduling and byte-identical
  existing CSV export at 390/1440 pixels. No browser errors or document overflow.
  Mobile screenshot reviewed. The first browser script used the wrong date-save
  button label; matching the actual label fixed the script without app changes.
- `node /tmp/sideyard-qa/history.cjs http://127.0.0.1:18894`: existing vehicle and
  equipment flow passed, including delayed JavaScript and no-JavaScript fallback.

## Staging evidence

- Source: `ae6f16ae9f9feaf52bf58093cf1f0c3b8c7a7c34`.
- Image: `ghcr.io/wmichelin/clubdue@sha256:a57a268f081b8f2c96550eb2d61b00e36faae855757662c0a62b27162f5d319c`.
- [CI](https://github.com/wmichelin/clubdue/actions/runs/34353916568) and
  [Deploy Staging](https://github.com/wmichelin/clubdue/actions/runs/34353916405)
  succeeded. Source/image read directly from the running staging container.
- `./scripts/smoke-test.sh staging`: passed.
- `node /tmp/sideyard-qa/visit-history.cjs https://clubdue-staging.michelindigitalmedia.com staging`
  and `node /tmp/sideyard-qa/history.cjs https://clubdue-staging.michelindigitalmedia.com staging`:
  both passed on isolated fictional demos at 390/1440 pixels. No browser errors,
  document overflow or unexpected staging log errors. Mobile screenshot reviewed.
- Local QA server and disposable database removed. No production promotion;
  its source remains `7d33ab7ff1c05a796bd351b647f81eea58729726`, gate `false`.
