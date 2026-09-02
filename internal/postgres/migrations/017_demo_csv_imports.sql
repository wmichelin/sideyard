-- Public-demo CSV previews stay server-side behind a short-lived opaque token.
-- The separate row counter prevents a few admitted requests from amplifying
-- into an unbounded amount of tenant data.

ALTER TABLE shops
    ADD COLUMN demo_import_rows_used INTEGER NOT NULL DEFAULT 0
        CHECK (demo_import_rows_used >= 0),
    ADD COLUMN demo_import_occurrences_used INTEGER NOT NULL DEFAULT 0
        CHECK (demo_import_occurrences_used >= 0);

CREATE TABLE demo_import_previews (
    token_hash TEXT PRIMARY KEY,
    shop_id UUID NOT NULL REFERENCES shops(id) ON DELETE CASCADE,
    csv_data BYTEA NOT NULL
        CHECK (octet_length(csv_data) BETWEEN 1 AND 131072),
    row_count INTEGER NOT NULL
        CHECK (row_count BETWEEN 1 AND 100),
    projected_occurrences INTEGER NOT NULL
        CHECK (projected_occurrences BETWEEN 0 AND 12100),
    projection_date DATE NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (shop_id)
);

CREATE INDEX demo_import_previews_expiry
    ON demo_import_previews (expires_at);
