CREATE TABLE renewal_tasks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(), shop_id UUID NOT NULL REFERENCES shops(id) ON DELETE CASCADE,
    agreement_id UUID NOT NULL REFERENCES agreements(id) ON DELETE CASCADE,
    milestone_days INT NOT NULL CHECK (milestone_days IN (90,60,30,14,7)),
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','contacted','renewed','lost')),
    notes TEXT, updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (shop_id, agreement_id, milestone_days)
);
