CREATE TABLE owner_provision_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    action TEXT NOT NULL CHECK (action IN ('provisioned', 'recovered')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX owner_provision_events_user_created_at
    ON owner_provision_events (user_id, created_at DESC);
