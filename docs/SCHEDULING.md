# Scheduling recurring service

Open **Schedule** from navigation or **Service work**. Each service has its own
customer, property, cadence, last completed date and next service date. Multiple
services at one property stay independent. The same dates appear inside the
agreement's Recurring services section.

The **Dashboard** also has a service calendar. Select a day in the month grid
to see its open recurring visits, customer and property details, and Change date
links. The grid counts every open visit, so weekly services appear each week.
Completed, skipped, paused and inactive work is excluded from these workload
counts. Busy days show 50 visits per page with More visits navigation.

Use the previous/next month arrows or Today to navigate. A day selection takes
you directly to its agenda; Back to month returns to the grid. After changing a
visit, View updated calendar opens its new date. Navigation works without
JavaScript. Dates beyond the 90-day planning window are marked as incomplete,
while visits already moved there remain visible. Browsing future months does
not generate extra work.

- **Last completed** is when completion was recorded, using UTC dates. Skipped
  visits do not count; no history is shown as “No completion recorded.” This
  feature does not edit historical completion dates.
- **Next service** is the earliest open visit, including overdue work. Paused
  and inactive services are identified explicitly. The app generates a rolling
  90-day window; a service beyond that window says no visit is available in the
  next 90 days.
- **Change date** moves the next visit. To move a later one, open Service work's
  All view and use that visit's Change date link. Review the current and original
  dates, choose the new date, then save. Cancel leaves the visit unchanged.
- A move affects **this visit only**. Later visits keep their cadence dates.
  Moving a visit past another open visit makes that earlier visit the next one.
  The due queue, overdue state and CSV export all use the moved date.
- A date must stay within the service's start/end dates and cannot collide with
  another visit of that service. Different customers may share a service date.
  Completed/skipped visits and inactive services cannot be moved. A stale tab
  must review the current date before retrying.
- Unchanged CSV imports preserve adjustments. Pausing a service or importing
  changed schedule terms resets pending future work, including visits with
  either a future original date or a future adjusted date. Resuming returns to
  the existing cadence. Completed and skipped history remains.

This is manual planning by date. Crew assignment, appointment times, route
optimization and moving an entire recurring series are separate capabilities.

## Release compatibility

Migration 018 adds a nullable planned-date override and revision counter. The
original recurrence date remains the identity used by generation, preventing
duplicate visits. Earlier binaries can start with the additive schema but ignore
overrides. Once visits have been moved, prefer fixing forward; a binary rollback
would display and act on original dates until this capability is restored. A
rollback does not undo the stored adjustments or roll back the database.
