CREATE TABLE dbo.property_photos (
    id uniqueidentifier NOT NULL PRIMARY KEY DEFAULT NEWID(),
    shop_id uniqueidentifier NOT NULL,
    property_id uniqueidentifier NOT NULL,
    submission_key nvarchar(65) COLLATE Latin1_General_100_BIN2 NOT NULL CHECK(LEN(submission_key COLLATE Latin1_General_100_CI_AS_SC+CONVERT(nvarchar(max),N'#'))-1=32),
    content_hash nvarchar(129) COLLATE Latin1_General_100_BIN2 NOT NULL CHECK(LEN(content_hash COLLATE Latin1_General_100_CI_AS_SC+CONVERT(nvarchar(max),N'#'))-1=64),
    caption nvarchar(max) NOT NULL CHECK(LEN(caption COLLATE Latin1_General_100_CI_AS_SC+N'#')-1<=500),
    category nvarchar(8) COLLATE Latin1_General_100_BIN2 NOT NULL CHECK(category IN(N'before',N'after',N'damage',N'access',N'general') AND DATALENGTH(category)=DATALENGTH(RTRIM(category))),
    content_type nvarchar(11) COLLATE Latin1_General_100_BIN2 NOT NULL CHECK(content_type IN(N'image/jpeg',N'image/png') AND DATALENGTH(content_type)=DATALENGTH(RTRIM(content_type))),
    image_data varbinary(max),
    recorded_by nvarchar(max) NOT NULL,
    recorded_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    deleted_at datetime2(6),
    object_key nvarchar(65) COLLATE Latin1_General_100_BIN2 CHECK(DATALENGTH(object_key)=128 AND object_key NOT LIKE N'%[^a-f0-9]%'),
    object_version nvarchar(max) COLLATE Latin1_General_100_BIN2,
    object_pending bit NOT NULL DEFAULT 0,
    FOREIGN KEY(shop_id,property_id) REFERENCES dbo.properties(shop_id,id) ON DELETE CASCADE,
    UNIQUE(shop_id,submission_key),
    CHECK((deleted_at IS NULL AND DATALENGTH(image_data) BETWEEN 1 AND 4194304 AND image_data IS NOT NULL)
       OR (deleted_at IS NOT NULL AND image_data IS NULL AND DATALENGTH(caption)=0)),
    CHECK(object_version IS NULL OR (object_key IS NOT NULL AND DATALENGTH(object_version)>0))
);
CREATE INDEX property_photos_active ON dbo.property_photos(shop_id,property_id,recorded_at DESC,id) WHERE deleted_at IS NULL;

-- Retirement inventory deliberately outlives every parent and contains no
-- caption, author or address. This is recovery bookkeeping, not object purging.
CREATE TABLE dbo.property_photo_objects (
    object_key nvarchar(65) COLLATE Latin1_General_100_BIN2 NOT NULL PRIMARY KEY CHECK(DATALENGTH(object_key)=128 AND object_key NOT LIKE N'%[^a-f0-9]%'),
    shop_id uniqueidentifier NOT NULL,
    photo_id uniqueidentifier NOT NULL UNIQUE,
    content_hash nvarchar(129) COLLATE Latin1_General_100_BIN2 NOT NULL CHECK(LEN(content_hash COLLATE Latin1_General_100_CI_AS_SC+CONVERT(nvarchar(max),N'#'))-1=64),
    content_type nvarchar(11) COLLATE Latin1_General_100_BIN2 NOT NULL CHECK(content_type IN(N'image/jpeg',N'image/png') AND DATALENGTH(content_type)=DATALENGTH(RTRIM(content_type))),
    byte_length int NOT NULL CHECK(byte_length BETWEEN 1 AND 4194304),
    version_id nvarchar(max) COLLATE Latin1_General_100_BIN2,
    created_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    retired_at datetime2(6),
    purged_at datetime2(6)
);
CREATE INDEX property_photo_objects_retired ON dbo.property_photo_objects(retired_at) WHERE retired_at IS NOT NULL AND purged_at IS NULL;
