# ClubDue: five-minute recurring-service walkthrough

For a prospect, open the permanent staging link:

<https://clubdue-staging.michelindigitalmedia.com/demo>

It enters a fresh fictional landscaping workspace without credentials. The
banner identifies the data as fictional and temporary. For the established
local HVAC fixture, follow the README seed/login steps instead.

## 0:00–0:45 — Start with the morning question

Open **Dashboard**. Frame ClubDue as a commitment book rather than another
dispatch or accounting suite:

- Active agreements is the current book of business.
- Renewals due and lapsed plans expose retention work.
- Services overdue links directly to commitments that need an outcome.

The useful question is: “Who have we promised work to, and what is at risk of
being forgotten?”

## 0:45–1:45 — Work the recurring queue

Open **Service work**. The fictional business includes weekly lawn care,
biweekly property care, and monthly irrigation checks. Show the customer,
property, actual service name, date, and due/overdue state.

Select one item and use **Mark completed** or **Mark skipped**. Refresh to show
that the outcome is retained. Explain the boundary honestly: ClubDue is not
optimizing routes, tracking GPS, or sending a technician automatically.

## 1:45–2:35 — Inspect and change a recurring commitment

Open the linked service plan. Under **Recurring services**, show the recurrence
and start/end boundary. Add a small example or pause/resume the existing
schedule. Recurrence is separate from renewal terms, so weekly work does not
pretend to be a spring/fall entitlement.

## 2:35–3:20 — Review renewal work

Open **Renewals**. Each row includes contact details, property, agreement,
anniversary, and an upcoming/due/past-due state. Record a non-final
**Contacted** outcome and a fictional note. ClubDue does not send outreach or
decide that a customer paid; outcomes are explicit owner records.

## 3:20–4:10 — Explain Sheets onboarding

Open **Import**. Uploads are disabled in the public demo so nobody can put real
customer data into a public sandbox. Explain the real pilot flow:

1. Export an authorized, sanitized Google Sheet as CSV.
2. Preview every row and correction.
3. Reconcile counts.
4. Commit only when errors are zero.
5. Re-import with the same stable external IDs to update, not duplicate.

Use `internal/app/testdata/landscaping-google-sheets-template.csv` in an
authenticated, non-public pilot workspace.

## 4:10–5:00 — Export and close on the boundary

Return to **Service work** and choose **Export CSV**. The export carries each
real service name for handoff to the owner's existing operational tool.

Close with what is being tested: whether a focused, spreadsheet-compatible
commitment loop prevents missed work with much less setup than a full suite.
Routing, dispatch, invoicing, payments, automated messaging, and direct Google
Sheets OAuth are deliberately not claimed.
