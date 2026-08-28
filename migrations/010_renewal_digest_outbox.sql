-- Provider-neutral handoff for the owner digest. A delivery worker can claim
-- queued rows later without rebuilding a changing renewal queue.
CREATE TABLE renewal_digest_outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    shop_id UUID NOT NULL REFERENCES shops(id) ON DELETE CASCADE,
    digest_date DATE NOT NULL,
    task_ids UUID[] NOT NULL CHECK (cardinality(task_ids) > 0),
    status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'sent', 'failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at TIMESTAMPTZ,
    UNIQUE (shop_id, digest_date)
);

CREATE INDEX renewal_digest_outbox_status ON renewal_digest_outbox (status, created_at)
    WHERE status IN ('queued', 'failed');
