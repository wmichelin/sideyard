-- SQL Server rejects the two cascading routes through vehicles and equipment.
-- Asset references are enforced by the following validation/cleanup triggers;
-- restore must fire them and audit ownership, not only CHECKCONSTRAINTS.
CREATE TABLE dbo.asset_notes (
    id uniqueidentifier NOT NULL PRIMARY KEY DEFAULT NEWID(),
    shop_id uniqueidentifier NOT NULL REFERENCES dbo.shops(id) ON DELETE CASCADE,
    vehicle_id uniqueidentifier,
    equipment_id uniqueidentifier,
    submission_key nvarchar(65) COLLATE Latin1_General_100_BIN2 NOT NULL CHECK(LEN(submission_key COLLATE Latin1_General_100_CI_AS_SC+CONVERT(nvarchar(max),N'#'))-1=32),
    body nvarchar(max) NOT NULL CHECK(LEN(body COLLATE Latin1_General_100_CI_AS_SC+N'#')-1 BETWEEN 1 AND 2000),
    occurred_on date NOT NULL,
    occurred_at datetime2(6),
    event_timezone nvarchar(max) NOT NULL CHECK(LEN(event_timezone COLLATE Latin1_General_100_CI_AS_SC+N'#')-1 BETWEEN 1 AND 100),
    recorded_by nvarchar(max) NOT NULL,
    recorded_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    CHECK((vehicle_id IS NOT NULL AND equipment_id IS NULL) OR (vehicle_id IS NULL AND equipment_id IS NOT NULL)),
    UNIQUE(shop_id,submission_key)
);
CREATE INDEX asset_notes_vehicle_history ON dbo.asset_notes(shop_id,vehicle_id,occurred_on DESC,recorded_at DESC,id DESC) WHERE vehicle_id IS NOT NULL;
CREATE INDEX asset_notes_equipment_history ON dbo.asset_notes(shop_id,equipment_id,occurred_on DESC,recorded_at DESC,id DESC) WHERE equipment_id IS NOT NULL;
