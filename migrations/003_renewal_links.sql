-- Phase 1.9: expiring homeowner renewal links. Only a keyed hash is stored.

CREATE TABLE renewal_links (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    shop_id UUID NOT NULL REFERENCES shops(id) ON DELETE CASCADE,
    agreement_id UUID NOT NULL REFERENCES agreements(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX renewal_links_token_expires ON renewal_links (token_hash, expires_at);
