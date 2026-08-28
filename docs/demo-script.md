# ClubDue: five-minute shop-owner walkthrough

This script uses the included demo shop. Before presenting, follow the “Local demo in five minutes” steps in the [README](../README.md), seed the database, and sign in as `owner@demo.local`.

## 0:00–0:45 — Open the dashboard

Start on **Dashboard**. Explain that this is the morning view for the agreement manager:

- Active agreements shows the current book of business.
- Renewals due this month and lapsed MTD highlight retention work.
- Visits overdue points directly to unscheduled tune-ups.

Click **Renewal queue** and then return to the dashboard.

## 0:45–1:30 — Review upcoming renewals

On **Renewals**, show the 7/14/30/60/90-day groups. Each row includes the homeowner, property, plan, anniversary, days remaining, and price. This makes it easy to work the most urgent calls first.

This MVP is a visibility queue; it does not yet record contact attempts or outcomes.

## 1:30–2:20 — Work visits and export to Jobber

Open **Visits**. Point out spring/fall due windows and the overdue emphasis. Select one or two visits and use **Mark scheduled** or **Mark completed**. Then choose **Export CSV** to demonstrate the Jobber-paste export.

## 2:20–3:15 — Add a plan, customer, and agreement

Open **Plan templates** and add a plan if needed. Then open **Customers** and add a homeowner. Go to **Agreements → New agreement**, select the customer and plan, enter an anniversary date and price, then save. The agreement appears in the book with visit entitlement and status.

## 3:15–4:00 — Bring in existing agreements

Open **Import**, upload `testdata/sample-import.csv`, and select **Preview import**. Call out the dry-run actions and error count. Choose **Commit import** to apply valid rows. Re-uploading the same file updates matching external IDs instead of creating duplicates.

## 4:00–5:00 — Send a homeowner renewal link

Open an agreement, scroll to **Homeowner renewal link**, and select **Create renewal link**. Open the generated link in a new tab. The customer-facing page shows the plan and price, then clearly labels payment as a non-charging test stub.

Close with the MVP boundary: the workflow proves the renewal handoff today; production Stripe Checkout and verified webhooks are the payment follow-up.
