ALTER TABLE visit_dues ADD CONSTRAINT visit_dues_shop_identity UNIQUE (shop_id,id);
ALTER TABLE service_occurrences ADD CONSTRAINT service_occurrences_shop_identity UNIQUE (shop_id,id);

CREATE TABLE visit_notes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    shop_id UUID NOT NULL REFERENCES shops(id) ON DELETE CASCADE,
    occurrence_id UUID,
    seasonal_visit_id UUID,
    submission_key TEXT NOT NULL CHECK (length(submission_key)=32),
    body TEXT NOT NULL CHECK (length(body) BETWEEN 1 AND 2000),
    occurred_on DATE NOT NULL,
    occurred_at TIMESTAMPTZ,
    event_timezone TEXT NOT NULL CHECK (length(event_timezone) BETWEEN 1 AND 100),
    recorded_by TEXT NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (num_nonnulls(occurrence_id,seasonal_visit_id)=1),
    FOREIGN KEY (shop_id,occurrence_id) REFERENCES service_occurrences(shop_id,id) ON DELETE CASCADE,
    FOREIGN KEY (shop_id,seasonal_visit_id) REFERENCES visit_dues(shop_id,id) ON DELETE CASCADE,
    UNIQUE (shop_id,submission_key)
);
CREATE INDEX visit_notes_occurrence_history ON visit_notes(shop_id,occurrence_id,occurred_on DESC,recorded_at DESC,id DESC) WHERE occurrence_id IS NOT NULL;
CREATE INDEX visit_notes_seasonal_history ON visit_notes(shop_id,seasonal_visit_id,occurred_on DESC,recorded_at DESC,id DESC) WHERE seasonal_visit_id IS NOT NULL;
