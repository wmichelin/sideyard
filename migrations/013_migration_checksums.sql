-- Migration checksums are populated by the application migrator. Keeping the
-- column migration here documents the durable schema contract; the migrator
-- creates it compatibly before it can verify earlier, already-applied files.
ALTER TABLE schema_migrations
    ADD COLUMN IF NOT EXISTS checksum TEXT;
