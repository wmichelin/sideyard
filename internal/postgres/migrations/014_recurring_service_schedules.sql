-- Add a bounded recurring-service capability without changing the established
-- seasonal HVAC visit workflow. Previous binaries ignore these additive tables.

CREATE TABLE service_schedules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    shop_id UUID NOT NULL REFERENCES shops(id) ON DELETE CASCADE,
    agreement_id UUID NOT NULL REFERENCES agreements(id) ON DELETE CASCADE,
    external_id TEXT,
    service_name TEXT NOT NULL,
    cadence_unit TEXT NOT NULL CHECK (cadence_unit IN ('day', 'week', 'month')),
    cadence_interval INT NOT NULL CHECK (cadence_interval > 0),
    starts_on DATE NOT NULL,
    ends_on DATE,
    generated_through DATE,
    active BOOLEAN NOT NULL DEFAULT true,
    paused_at TIMESTAMPTZ,
    counts_toward_entitlement BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (ends_on IS NULL OR ends_on >= starts_on),
    UNIQUE (shop_id, external_id),
    UNIQUE (shop_id, id)
);

CREATE INDEX service_schedules_shop_active
    ON service_schedules (shop_id, active);
CREATE INDEX service_schedules_agreement_id
    ON service_schedules (agreement_id);

CREATE OR REPLACE FUNCTION validate_service_schedule_agreement()
RETURNS TRIGGER AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM agreements
        WHERE id = NEW.agreement_id AND shop_id = NEW.shop_id
    ) THEN
        RAISE EXCEPTION 'service schedule agreement must belong to the shop'
            USING ERRCODE = 'foreign_key_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER service_schedules_validate_agreement
    BEFORE INSERT OR UPDATE OF shop_id, agreement_id
    ON service_schedules
    FOR EACH ROW EXECUTE FUNCTION validate_service_schedule_agreement();

CREATE TABLE service_occurrences (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    shop_id UUID NOT NULL REFERENCES shops(id) ON DELETE CASCADE,
    schedule_id UUID NOT NULL REFERENCES service_schedules(id) ON DELETE CASCADE,
    due_on DATE NOT NULL,
    status TEXT NOT NULL DEFAULT 'due'
        CHECK (status IN ('due', 'completed', 'skipped')),
    completed_at TIMESTAMPTZ,
    notes TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (shop_id, schedule_id, due_on)
);

CREATE INDEX service_occurrences_shop_due
    ON service_occurrences (shop_id, due_on)
    WHERE status = 'due';

CREATE OR REPLACE FUNCTION validate_service_occurrence_schedule()
RETURNS TRIGGER AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM service_schedules
        WHERE id = NEW.schedule_id AND shop_id = NEW.shop_id
    ) THEN
        RAISE EXCEPTION 'service occurrence schedule must belong to the shop'
            USING ERRCODE = 'foreign_key_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER service_occurrences_validate_schedule
    BEFORE INSERT OR UPDATE OF shop_id, schedule_id
    ON service_occurrences
    FOR EACH ROW EXECUTE FUNCTION validate_service_occurrence_schedule();
