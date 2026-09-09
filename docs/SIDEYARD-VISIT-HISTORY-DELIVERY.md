# Visit-linked internal notes

Status: testing
Last completed: implementation, full checks and local browser acceptance passed.
Now: publishing authored notes tied to existing visit identities.
Next: CI, staging smoke and browser verification.
Staging: https://clubdue-staging.michelindigitalmedia.com (pending for this slice)
Blocker: none for development. Production scope clarification is pending.

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
