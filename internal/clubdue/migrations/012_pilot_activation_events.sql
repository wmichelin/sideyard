-- Privacy-safe, first-occurrence milestones for the controlled pilot. This
-- table intentionally stores no customer, operator, device, or free-text data.
CREATE TABLE pilot_activation_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    shop_id UUID NOT NULL REFERENCES shops(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL CHECK (event_type IN (
        'invited', 'account_created', 'import_previewed', 'import_committed',
        'first_renewal_action', 'first_visit_action', 'returning_use'
    )),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (shop_id, event_type)
);

CREATE INDEX pilot_activation_events_shop_occurred_at
    ON pilot_activation_events (shop_id, occurred_at);
