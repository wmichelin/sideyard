-- due_on remains the immutable cadence identity used by generation/deduplication.
-- Older binaries can read this schema but ignore manual scheduling overrides.
ALTER TABLE service_occurrences
    ADD COLUMN scheduled_on DATE,
    ADD COLUMN schedule_revision INT NOT NULL DEFAULT 0;

CREATE INDEX service_occurrences_effective_date
    ON service_occurrences (shop_id, schedule_id, (COALESCE(scheduled_on, due_on)))
    WHERE status = 'due';
