CREATE TABLE dbo.property_details (
    shop_id uniqueidentifier NOT NULL,
    property_id uniqueidentifier NOT NULL PRIMARY KEY,
    access_notes nvarchar(max) NOT NULL DEFAULT N'' CHECK(LEN(access_notes COLLATE Latin1_General_100_CI_AS_SC+N'#')-1<=2000),
    revision int NOT NULL DEFAULT 1 CHECK(revision>0),
    FOREIGN KEY(shop_id,property_id) REFERENCES dbo.properties(shop_id,id) ON DELETE CASCADE
);

CREATE TABLE dbo.property_measurements (
    id uniqueidentifier NOT NULL PRIMARY KEY DEFAULT NEWID(),
    shop_id uniqueidentifier NOT NULL,
    property_id uniqueidentifier NOT NULL,
    kind nvarchar(19) COLLATE Latin1_General_100_BIN2 NOT NULL CHECK(kind IN (N'turf',N'beds',N'driveway',N'walkway',N'patio',N'roof',N'building_footprint',N'floor_area',N'exterior_wall',N'perimeter',N'other_area',N'other_length') AND DATALENGTH(kind)=DATALENGTH(RTRIM(kind))),
    zone nvarchar(max) NOT NULL CHECK(LEN(zone COLLATE Latin1_General_100_CI_AS_SC+N'#')-1 BETWEEN 1 AND 120),
    value decimal(14,3) NOT NULL CHECK(value>0),
    unit nvarchar(6) COLLATE Latin1_General_100_BIN2 NOT NULL CHECK(unit IN (N'sq_ft',N'sq_m',N'ft',N'm') AND DATALENGTH(unit)=DATALENGTH(RTRIM(unit))),
    source nvarchar(max) NOT NULL CHECK(LEN(source COLLATE Latin1_General_100_CI_AS_SC+N'#')-1 BETWEEN 1 AND 120),
    measured_on date NOT NULL,
    estimated bit NOT NULL,
    recorded_by nvarchar(max) NOT NULL,
    recorded_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    FOREIGN KEY(shop_id,property_id) REFERENCES dbo.properties(shop_id,id) ON DELETE CASCADE,
    CHECK((kind IN (N'perimeter',N'other_length') AND unit IN (N'ft',N'm')) OR
          (kind NOT IN (N'perimeter',N'other_length') AND unit IN (N'sq_ft',N'sq_m')))
);
CREATE INDEX property_measurements_history ON dbo.property_measurements(shop_id,property_id,recorded_at DESC,id);

CREATE TABLE dbo.property_notes (
    id uniqueidentifier NOT NULL PRIMARY KEY DEFAULT NEWID(),
    shop_id uniqueidentifier NOT NULL,
    property_id uniqueidentifier NOT NULL,
    submission_key nvarchar(65) COLLATE Latin1_General_100_BIN2 NOT NULL CHECK(LEN(submission_key COLLATE Latin1_General_100_CI_AS_SC+CONVERT(nvarchar(max),N'#'))-1=32),
    body nvarchar(max) NOT NULL CHECK(LEN(body COLLATE Latin1_General_100_CI_AS_SC+N'#')-1 BETWEEN 1 AND 2000),
    occurred_on date NOT NULL,
    recorded_by nvarchar(max) NOT NULL,
    recorded_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    UNIQUE(shop_id,submission_key),
    FOREIGN KEY(shop_id,property_id) REFERENCES dbo.properties(shop_id,id) ON DELETE CASCADE
);
CREATE INDEX property_notes_history ON dbo.property_notes(shop_id,property_id,recorded_at DESC,id DESC);
