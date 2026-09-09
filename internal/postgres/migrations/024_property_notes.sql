CREATE TABLE property_notes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    shop_id UUID NOT NULL REFERENCES shops(id) ON DELETE CASCADE,
    property_id UUID NOT NULL,
    submission_key TEXT NOT NULL CHECK (length(submission_key)=32),
    body TEXT NOT NULL CHECK (length(body) BETWEEN 1 AND 2000),
    occurred_on DATE NOT NULL,
    recorded_by TEXT NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (shop_id, property_id) REFERENCES properties(shop_id, id) ON DELETE CASCADE,
    UNIQUE (shop_id, submission_key)
);
CREATE INDEX property_notes_history ON property_notes(shop_id,property_id,recorded_at DESC,id DESC);
