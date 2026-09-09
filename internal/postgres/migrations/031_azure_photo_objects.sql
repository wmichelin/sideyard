-- Keep inline recovery bytes while Azure photo persistence is rolled out.
-- Existing backups and previous app releases remain usable during this phase.
ALTER TABLE property_photos
    ADD COLUMN object_key TEXT CHECK (object_key ~ '^[a-f0-9]{64}$'),
    ADD COLUMN object_version TEXT,
    ADD COLUMN object_pending BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE property_photos ADD CONSTRAINT property_photos_object_reference
    CHECK (object_version IS NULL OR (object_key IS NOT NULL AND length(object_version) > 0));

-- Deliberately no cascading foreign keys: object retirement must survive deletion
-- of a property, customer or entire shop. No caption, author or address is kept.
CREATE TABLE property_photo_objects (
    object_key TEXT PRIMARY KEY CHECK (object_key ~ '^[a-f0-9]{64}$'),
    shop_id UUID NOT NULL,
    photo_id UUID NOT NULL UNIQUE,
    content_hash TEXT NOT NULL CHECK (length(content_hash) = 64),
    content_type TEXT NOT NULL CHECK (content_type IN ('image/jpeg','image/png')),
    byte_length INTEGER NOT NULL CHECK (byte_length BETWEEN 1 AND 4194304),
    version_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    retired_at TIMESTAMPTZ,
    purged_at TIMESTAMPTZ
);
CREATE INDEX property_photo_objects_retired ON property_photo_objects(retired_at)
    WHERE retired_at IS NOT NULL AND purged_at IS NULL;

CREATE FUNCTION retire_property_photo_object() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        UPDATE property_photo_objects SET retired_at=COALESCE(retired_at,now())
        WHERE object_key=OLD.object_key;
    ELSIF OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL THEN
        UPDATE property_photo_objects SET retired_at=COALESCE(retired_at,now())
        WHERE object_key=OLD.object_key;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER property_photos_retire_object
    AFTER DELETE OR UPDATE ON property_photos
    FOR EACH ROW EXECUTE FUNCTION retire_property_photo_object();
