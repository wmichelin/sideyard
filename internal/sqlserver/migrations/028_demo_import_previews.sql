-- Preview hashes are opaque historical identities, not restricted to today's
-- generated 64-character format. One active preview per shop remains enforced.
CREATE TABLE dbo.demo_import_previews (
    token_hash nvarchar(max) COLLATE Latin1_General_100_BIN2 NOT NULL,
    token_key AS CONVERT(binary(32),HASHBYTES('SHA2_256',CONVERT(varbinary(max),token_hash))) PERSISTED,
    shop_id uniqueidentifier NOT NULL REFERENCES dbo.shops(id) ON DELETE CASCADE,
    csv_data varbinary(max) NOT NULL CHECK (DATALENGTH(csv_data) BETWEEN 1 AND 131072),
    row_count int NOT NULL CHECK (row_count BETWEEN 1 AND 100),
    projected_occurrences int NOT NULL CHECK (projected_occurrences BETWEEN 0 AND 12100),
    projection_date date NOT NULL,
    expires_at datetime2(6) NOT NULL,
    created_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    CONSTRAINT demo_import_previews_token_key UNIQUE (token_key),
    CONSTRAINT demo_import_previews_shop UNIQUE (shop_id)
);
CREATE INDEX demo_import_previews_expiry ON dbo.demo_import_previews(expires_at);
