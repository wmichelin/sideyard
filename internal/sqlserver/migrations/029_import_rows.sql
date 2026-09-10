-- Historical polymorphic references deliberately have no entity foreign key.
CREATE TABLE dbo.import_rows (
    id uniqueidentifier NOT NULL PRIMARY KEY DEFAULT NEWID(),
    shop_id uniqueidentifier NOT NULL REFERENCES dbo.shops(id) ON DELETE CASCADE,
    source nvarchar(max) COLLATE Latin1_General_100_BIN2 NOT NULL,
    external_id nvarchar(max) COLLATE Latin1_General_100_BIN2 NOT NULL,
    entity_type nvarchar(max) COLLATE Latin1_General_100_BIN2 NOT NULL,
    source_key AS CONVERT(binary(32),HASHBYTES('SHA2_256',CONVERT(varbinary(max),source))) PERSISTED,
    external_id_key AS CONVERT(binary(32),HASHBYTES('SHA2_256',CONVERT(varbinary(max),external_id))) PERSISTED,
    entity_type_key AS CONVERT(binary(32),HASHBYTES('SHA2_256',CONVERT(varbinary(max),entity_type))) PERSISTED,
    entity_id uniqueidentifier NOT NULL,
    content_hash nvarchar(max),
    imported_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    UNIQUE(shop_id,source_key,external_id_key,entity_type_key)
);
