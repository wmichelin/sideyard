CREATE TABLE dbo.business_settings (
    shop_id uniqueidentifier NOT NULL PRIMARY KEY REFERENCES dbo.shops(id) ON DELETE CASCADE,
    primary_trade nvarchar(max) COLLATE Latin1_General_100_BIN2 NOT NULL,
    trades nvarchar(max) NOT NULL CHECK(ISJSON(trades,ARRAY)=1),
    revision int NOT NULL DEFAULT 1 CHECK(revision>0)
);

CREATE TABLE dbo.business_services (
    id uniqueidentifier NOT NULL PRIMARY KEY DEFAULT NEWID(),
    shop_id uniqueidentifier NOT NULL REFERENCES dbo.shops(id) ON DELETE CASCADE,
    preset_key nvarchar(max) COLLATE Latin1_General_100_BIN2 NOT NULL,
    preset_key_hash AS CONVERT(binary(32),HASHBYTES('SHA2_256',CONVERT(varbinary(max),preset_key))) PERSISTED,
    preset_version int NOT NULL CHECK(preset_version>0),
    name nvarchar(max) NOT NULL CHECK(LEN(name COLLATE Latin1_General_100_CI_AS_SC+N'#')-1 BETWEEN 1 AND 120),
    description nvarchar(max) NOT NULL DEFAULT N'',
    enabled bit NOT NULL DEFAULT 1,
    revision int NOT NULL DEFAULT 1 CHECK(revision>0),
    UNIQUE(shop_id,preset_key_hash)
);
