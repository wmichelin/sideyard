# ClubDue: five-minute recurring-service walkthrough

For a prospect, open the permanent production link:

<https://clubdue.michelindigitalmedia.com/demo>

It enters a fresh fictional landscaping workspace without credentials. The
banner identifies the data as fictional and temporary. Use
<https://clubdue-staging.michelindigitalmedia.com/demo> only for release review.
For the established local HVAC fixture, follow the README seed/login steps
instead.

## 0:00–0:45 — Start with the morning question

Open **Dashboard**. Frame ClubDue as a commitment book rather than another
dispatch or accounting suite:

- Active agreements is the current book of business.
- Renewals due and lapsed plans expose retention work.
- Services overdue links directly to commitments that need an outcome.

The useful question is: “Who have we promised work to, and what is at risk of
being forgotten?”

The fresh workspace contains 30 deliberately fictional customer records: a
mostly residential book plus small commercial, HOA, and property-management
accounts; incomplete contacts; multi-location accounts; and every agreement
state. The recurring and renewal queues are intentionally smaller than the
customer book so the work view remains useful rather than becoming a data dump.

## 0:45–1:45 — Work the recurring queue

Open **Service work**. The fictional business includes weekly lawn care,
biweekly property care, monthly irrigation checks, paused routes, seasonal
visits, and retained inactive-plan history. Show the customer, property, actual
service name, date, and due/overdue state.

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

## 3:20–4:10 — Try Sheets onboarding

Open **Import**. Use only the fictional landscaping sample or a properly
redacted export—never a real customer sheet. The public demo accepts one `.csv`
through this exact preview-and-commit flow, capped at 100 data rows, 128 KiB raw,
and 2,000 bytes per cell. A sandbox can admit at most 500 uploaded data rows
and a combined 20,000 projected recurring visits across committed imports over
its four-hour lifetime.

1. Choose the fictional `.csv` and select **Preview import**. Preview creates no
   customer, property, agreement, or service records; it protects the exact CSV
   server-side for up to 15 minutes.
2. Review every displayed correction and reconcile the counts.
3. Commit only when errors are zero. The commit uses an expiring,
   tenant-bound, single-use token for the exact preview.
4. Explain that preview and commit each use one of the demo's 30 saved actions.
5. Re-importing the same stable external IDs updates rather than duplicates.

Every other multipart upload remains blocked. Access to a demo ends after four
hours and scheduled cleanup removes expired live data, but encrypted backups
may retain submitted data for up to 30 days. Never use real customer data; it
belongs only in an authenticated, non-public pilot workspace.

## 4:10–5:00 — Export and close on the boundary

Return to **Service work** and choose **Export CSV**. The export carries each
real service name for handoff to the owner's existing operational tool.

Close with what is being tested: whether a focused, spreadsheet-compatible
commitment loop prevents missed work with much less setup than a full suite.
Routing, dispatch, invoicing, payments, automated messaging, and direct Google
Sheets OAuth are deliberately not claimed.
