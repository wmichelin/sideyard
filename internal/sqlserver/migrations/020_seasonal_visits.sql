CREATE TABLE dbo.visit_dues (
    id uniqueidentifier NOT NULL PRIMARY KEY DEFAULT NEWID(),
    shop_id uniqueidentifier NOT NULL,
    agreement_id uniqueidentifier NOT NULL,
    season nvarchar(7) COLLATE Latin1_General_100_BIN2 NOT NULL
        CHECK(season IN(N'spring',N'fall') AND DATALENGTH(season)=DATALENGTH(RTRIM(season))),
    due_year int NOT NULL,
    status nvarchar(10) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'due'
        CHECK(status IN(N'due',N'scheduled',N'completed') AND DATALENGTH(status)=DATALENGTH(RTRIM(status))),
    scheduled_at datetime2(6), completed_at datetime2(6), notes nvarchar(max),
    created_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    UNIQUE(shop_id,id), UNIQUE(shop_id,agreement_id,season,due_year),
    FOREIGN KEY(shop_id,agreement_id) REFERENCES dbo.agreements(shop_id,id) ON DELETE CASCADE
);
CREATE INDEX visit_dues_shop_status ON dbo.visit_dues(shop_id,status);
CREATE INDEX visit_dues_agreement ON dbo.visit_dues(shop_id,agreement_id);
