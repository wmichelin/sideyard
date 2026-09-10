CREATE TABLE dbo.plan_templates (
    id uniqueidentifier NOT NULL PRIMARY KEY DEFAULT NEWID(),
    shop_id uniqueidentifier NOT NULL REFERENCES dbo.shops(id) ON DELETE CASCADE,
    name nvarchar(max) COLLATE Latin1_General_100_BIN2 NOT NULL,
    name_key AS CONVERT(binary(32),HASHBYTES('SHA2_256',CONVERT(varbinary(max),name))) PERSISTED,
    description nvarchar(max),
    price_cents int NOT NULL DEFAULT 0,
    billing_interval nvarchar(8) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'annual'
        CHECK(billing_interval IN(N'monthly',N'annual') AND DATALENGTH(billing_interval)=DATALENGTH(RTRIM(billing_interval))),
    term_months int NOT NULL DEFAULT 12 CHECK(term_months>0),
    visits_per_year int NOT NULL DEFAULT 2,
    visit_windows nvarchar(max) NOT NULL DEFAULT N'["spring","fall"]' CHECK(ISJSON(visit_windows,ARRAY)=1),
    created_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    UNIQUE(shop_id,name_key), UNIQUE(shop_id,id)
);

CREATE TABLE dbo.agreements (
    id uniqueidentifier NOT NULL PRIMARY KEY DEFAULT NEWID(),
    shop_id uniqueidentifier NOT NULL REFERENCES dbo.shops(id) ON DELETE CASCADE,
    customer_id uniqueidentifier NOT NULL,
    property_id uniqueidentifier,
    plan_template_id uniqueidentifier,
    external_id nvarchar(max) COLLATE Latin1_General_100_BIN2,
    external_id_key AS CONVERT(binary(32),HASHBYTES('SHA2_256',CONVERT(varbinary(max),external_id))) PERSISTED,
    status nvarchar(16) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'active'
        CHECK(status IN(N'active',N'past_due',N'lapsed',N'cancelled',N'pending_renewal') AND DATALENGTH(status)=DATALENGTH(RTRIM(status))),
    anniversary_date date NOT NULL,
    visits_entitled int NOT NULL DEFAULT 2,
    visits_used int NOT NULL DEFAULT 0,
    price_cents int NOT NULL DEFAULT 0,
    notes nvarchar(max),
    created_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    updated_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    plan_snapshot_name nvarchar(max) NOT NULL DEFAULT N'Custom plan',
    plan_snapshot_description nvarchar(max) NOT NULL DEFAULT N'',
    plan_snapshot_billing_interval nvarchar(8) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'annual'
        CHECK(plan_snapshot_billing_interval IN(N'monthly',N'annual') AND DATALENGTH(plan_snapshot_billing_interval)=DATALENGTH(RTRIM(plan_snapshot_billing_interval))),
    plan_snapshot_term_months int NOT NULL DEFAULT 12 CHECK(plan_snapshot_term_months>0),
    plan_snapshot_visits_per_year int NOT NULL DEFAULT 0 CHECK(plan_snapshot_visits_per_year>=0),
    plan_snapshot_visit_windows nvarchar(max) NOT NULL DEFAULT N'["spring","fall"]' CHECK(ISJSON(plan_snapshot_visit_windows,ARRAY)=1),
    UNIQUE(shop_id,id),
    FOREIGN KEY(shop_id,customer_id) REFERENCES dbo.customers(shop_id,id)
);
CREATE UNIQUE INDEX agreements_external_identity ON dbo.agreements(shop_id,external_id_key) WHERE external_id IS NOT NULL;
CREATE INDEX agreements_shop_anniversary ON dbo.agreements(shop_id,anniversary_date) WHERE status=N'active';
CREATE INDEX agreements_shop_status ON dbo.agreements(shop_id,status);
CREATE INDEX agreements_customer ON dbo.agreements(shop_id,customer_id);
CREATE INDEX agreements_property ON dbo.agreements(property_id,shop_id,customer_id) WHERE property_id IS NOT NULL;
CREATE INDEX agreements_template ON dbo.agreements(plan_template_id,shop_id) WHERE plan_template_id IS NOT NULL;
