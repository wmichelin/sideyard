CREATE TABLE dbo.service_schedules (
    id uniqueidentifier NOT NULL PRIMARY KEY DEFAULT NEWID(),
    shop_id uniqueidentifier NOT NULL,
    agreement_id uniqueidentifier NOT NULL,
    external_id nvarchar(max) COLLATE Latin1_General_100_BIN2,
    external_id_key AS CONVERT(binary(32),HASHBYTES('SHA2_256',CONVERT(varbinary(max),external_id))) PERSISTED,
    service_name nvarchar(max) NOT NULL,
    cadence_unit nvarchar(6) COLLATE Latin1_General_100_BIN2 NOT NULL
        CHECK(cadence_unit IN(N'day',N'week',N'month') AND DATALENGTH(cadence_unit)=DATALENGTH(RTRIM(cadence_unit))),
    cadence_interval int NOT NULL CHECK(cadence_interval>0),
    starts_on date NOT NULL, ends_on date, generated_through date,
    active bit NOT NULL DEFAULT 1, paused_at datetime2(6),
    counts_toward_entitlement bit NOT NULL DEFAULT 0,
    created_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    updated_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    UNIQUE(shop_id,id), CHECK(ends_on>=starts_on),
    FOREIGN KEY(shop_id,agreement_id) REFERENCES dbo.agreements(shop_id,id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX service_schedules_external_identity ON dbo.service_schedules(shop_id,external_id_key) WHERE external_id IS NOT NULL;
CREATE INDEX service_schedules_agreement ON dbo.service_schedules(shop_id,agreement_id);
CREATE TABLE dbo.service_occurrences (
    id uniqueidentifier NOT NULL PRIMARY KEY DEFAULT NEWID(),
    shop_id uniqueidentifier NOT NULL, schedule_id uniqueidentifier NOT NULL,
    due_on date NOT NULL, scheduled_on date,
    effective_on AS ISNULL(scheduled_on,due_on) PERSISTED,
    schedule_revision int NOT NULL DEFAULT 0,
    status nvarchar(10) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'due'
        CHECK(status IN(N'due',N'completed',N'skipped') AND DATALENGTH(status)=DATALENGTH(RTRIM(status))),
    completed_at datetime2(6), notes nvarchar(max),
    created_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    updated_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    UNIQUE(shop_id,id), UNIQUE(shop_id,schedule_id,due_on),
    FOREIGN KEY(shop_id,schedule_id) REFERENCES dbo.service_schedules(shop_id,id) ON DELETE CASCADE
);
CREATE INDEX service_occurrences_due ON dbo.service_occurrences(shop_id,effective_on) WHERE status=N'due';
CREATE INDEX service_schedules_active ON dbo.service_schedules(shop_id,active);
CREATE INDEX service_occurrences_schedule_due ON dbo.service_occurrences(shop_id,schedule_id,effective_on) WHERE status=N'due';
