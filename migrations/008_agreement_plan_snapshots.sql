-- Agreements are contractual records. Copy the plan terms onto each agreement
-- so a later template edit cannot rewrite its history, dashboard, renewal, or
-- visit behavior.
ALTER TABLE agreements
    ADD COLUMN plan_snapshot_name TEXT NOT NULL DEFAULT 'Custom plan',
    ADD COLUMN plan_snapshot_description TEXT NOT NULL DEFAULT '',
    ADD COLUMN plan_snapshot_billing_interval TEXT NOT NULL DEFAULT 'annual'
        CHECK (plan_snapshot_billing_interval IN ('monthly', 'annual')),
    ADD COLUMN plan_snapshot_term_months INT NOT NULL DEFAULT 12
        CHECK (plan_snapshot_term_months > 0),
    ADD COLUMN plan_snapshot_visits_per_year INT NOT NULL DEFAULT 0
        CHECK (plan_snapshot_visits_per_year >= 0),
    ADD COLUMN plan_snapshot_visit_windows TEXT[] NOT NULL DEFAULT '{spring,fall}';

-- One-time backfill: existing agreement price and visit counters are already
-- agreement facts; the remaining terms come from the template as it existed
-- at the time this migration is applied. Future creates snapshot atomically.
UPDATE agreements a
SET plan_snapshot_name = pt.name,
    plan_snapshot_description = COALESCE(pt.description, ''),
    plan_snapshot_billing_interval = pt.billing_interval,
    plan_snapshot_term_months = pt.term_months,
    plan_snapshot_visits_per_year = pt.visits_per_year,
    plan_snapshot_visit_windows = pt.visit_windows
FROM plan_templates pt
WHERE pt.id = a.plan_template_id;
