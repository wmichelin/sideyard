CREATE TABLE property_photos (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    shop_id UUID NOT NULL REFERENCES shops(id) ON DELETE CASCADE,
    property_id UUID NOT NULL,
    submission_key TEXT NOT NULL CHECK (length(submission_key) = 32),
    content_hash TEXT NOT NULL CHECK (length(content_hash) = 64),
    caption TEXT NOT NULL CHECK (length(caption) <= 500),
    category TEXT NOT NULL CHECK (category IN ('before','after','damage','access','general')),
    content_type TEXT NOT NULL CHECK (content_type IN ('image/jpeg','image/png')),
    image_data BYTEA,
    recorded_by TEXT NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    FOREIGN KEY (shop_id,property_id) REFERENCES properties(shop_id,id) ON DELETE CASCADE,
    UNIQUE (shop_id,submission_key),
    CHECK ((deleted_at IS NULL AND octet_length(image_data) BETWEEN 1 AND 4194304 AND image_data IS NOT NULL)
        OR (deleted_at IS NOT NULL AND image_data IS NULL AND caption = ''))
);
CREATE INDEX property_photos_active ON property_photos(shop_id,property_id,recorded_at DESC,id) WHERE deleted_at IS NULL;
