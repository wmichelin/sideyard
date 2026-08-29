-- Visit due-list for seasonal tune-ups (spring/fall)

CREATE TABLE visit_dues (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    shop_id UUID NOT NULL REFERENCES shops(id) ON DELETE CASCADE,
    agreement_id UUID NOT NULL REFERENCES agreements(id) ON DELETE CASCADE,
    season TEXT NOT NULL CHECK (season IN ('spring', 'fall')),
    due_year INT NOT NULL,
    status TEXT NOT NULL DEFAULT 'due'
        CHECK (status IN ('due', 'scheduled', 'completed')),
    scheduled_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    notes TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (shop_id, agreement_id, season, due_year)
);

CREATE INDEX visit_dues_shop_status ON visit_dues (shop_id, status);
CREATE INDEX visit_dues_agreement_id ON visit_dues (agreement_id);
