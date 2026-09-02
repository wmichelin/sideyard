-- Public demos are isolated, expiring tenants. Customer shops keep the default
-- kind and are never selected by the demo cleanup path.

ALTER TABLE shops
    ADD COLUMN kind TEXT NOT NULL DEFAULT 'customer'
        CHECK (kind IN ('customer', 'demo_sandbox')),
    ADD COLUMN expires_at TIMESTAMPTZ,
    ADD CONSTRAINT shops_demo_expiry_required CHECK (
        (kind = 'customer' AND expires_at IS NULL) OR
        (kind = 'demo_sandbox' AND expires_at IS NOT NULL)
    );

CREATE INDEX shops_demo_expiry
    ON shops (expires_at)
    WHERE kind = 'demo_sandbox';
