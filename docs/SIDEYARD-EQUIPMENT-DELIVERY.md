# Equipment records delivery

Status: testing
Last completed: implementation, full checks and local browser walkthrough passed.
Now: committing verified changes for staging.
Next: staging deployment and phone/desktop verification.
Staging: https://clubdue-staging.michelindigitalmedia.com (pending for this slice)
Blocker: none. Production requires separate approval.

## Delivery brief

Continue E2.1 with editable equipment records for any trade: equipment type/name,
make, model, serial/reference, location within the property, installation date,
warranty end date and internal service details. Reuse existing equipment identity
and installation dates. Owners can open an existing item from its customer and
property. Preserve basic equipment creation and existing staff access to basic
equipment summaries. New private fields are owner-only.

## Team review

- Product manager: make existing items editable; unknown dates remain blank.
  Acceptance: two pieces of equipment retain independent details after editing,
  including optional dates. Photos, warranty alerts and work scheduling are out
  of scope for this slice.
- Architect: extend the existing table additively; do not copy equipment into a
  second asset table. Scope reads/updates to both shop and property ownership.
  Add a composite property ownership foreign key and optimistic revision check.
  Keep internal fields out of existing lists and exports.
- Unit-testing expert: cover legacy equipment identity/date preservation, stale
  and concurrent updates, optional/invalid dates, foreign tenants, staff denial,
  and escaped private content. Use real Postgres for constraints and races.
- Staff software engineer: add a focused owner-only GET/POST record route, linked
  from the existing customer equipment list and property record. Use current
  HTML form and CSRF conventions; validation errors must retain entered values.
- DevOps engineer: no new runtime service or secret. Additive migration supports
  binary rollback and existing backup/restore. Run CI, both rehearsals, staging
  smoke and deployed revision verification before reporting success.
- QA tester: create two synthetic items, edit each, clear an optional date,
  simulate an outdated tab, and inspect 390/1440-pixel layouts on staging.

Decision: proceed with this bounded equipment slice. Existing basic creation
permissions are unchanged; broader crew permissions remain E3.3. Internal details
are current editable information; authored equipment event history remains E2.3.

## Local verification

- Focused equipment, CSRF and public-demo request tests passed in Go 1.23 with
  real Postgres 16. Tests include concurrent saves, stale/invalid form input
  retention, two independent units, optional date clearing, existing installation
  dates, cross-tenant database constraints and owner/staff boundaries.
- `go vet ./...`, `go test -count=1 ./...` with required Postgres integration,
  and `CGO_ENABLED=0 go build -o /qa/sideyard-equipment-qa ./cmd/clubdue` passed.
- `./scripts/production-rehearsal.sh` and
  `./scripts/pilot-onboarding-rehearsal.sh` passed.
- `node /tmp/sideyard-qa/equipment.cjs` passed the real browser flow at 390/1440
  pixels, including separate records, dates, old-tab conflict and property links.
  No document overflow or browser errors; phone screenshot inspected.
- Local demo startup was rejected by the existing development-environment guard.
  Used a synthetic local owner for browser verification; no app policy changed.
