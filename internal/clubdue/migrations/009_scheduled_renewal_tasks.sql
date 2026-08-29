-- Renewal tasks belong to an agreement term, not merely an agreement. Persist
-- the actionable date and cycle so a future term receives a fresh cadence.
ALTER TABLE renewal_tasks
    ADD COLUMN cycle_start_date DATE,
    ADD COLUMN due_at DATE;

UPDATE renewal_tasks t
SET cycle_start_date = a.anniversary_date,
    due_at = a.anniversary_date - t.milestone_days
FROM agreements a
WHERE a.id = t.agreement_id;

ALTER TABLE renewal_tasks
    ALTER COLUMN cycle_start_date SET NOT NULL,
    ALTER COLUMN due_at SET NOT NULL,
    DROP CONSTRAINT renewal_tasks_shop_id_agreement_id_milestone_days_key,
    ADD CONSTRAINT renewal_tasks_shop_agreement_cycle_milestone_key
        UNIQUE (shop_id, agreement_id, cycle_start_date, milestone_days);

CREATE INDEX renewal_tasks_shop_due_at ON renewal_tasks (shop_id, due_at)
    WHERE status IN ('open', 'contacted');
