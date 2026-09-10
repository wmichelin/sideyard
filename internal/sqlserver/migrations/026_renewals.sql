CREATE TABLE dbo.renewal_links (
    id uniqueidentifier NOT NULL PRIMARY KEY DEFAULT NEWID(),
    shop_id uniqueidentifier NOT NULL, agreement_id uniqueidentifier NOT NULL,
    token_hash nvarchar(max) COLLATE Latin1_General_100_BIN2 NOT NULL,
    token_key AS CONVERT(binary(32),HASHBYTES('SHA2_256',CONVERT(varbinary(max),token_hash))) PERSISTED,
    expires_at datetime2(6) NOT NULL,
    created_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    UNIQUE(token_key),
    FOREIGN KEY(shop_id,agreement_id) REFERENCES dbo.agreements(shop_id,id) ON DELETE CASCADE
);
CREATE INDEX renewal_links_agreement ON dbo.renewal_links(shop_id,agreement_id);
CREATE TABLE dbo.renewal_tasks (
    id uniqueidentifier NOT NULL PRIMARY KEY DEFAULT NEWID(),
    shop_id uniqueidentifier NOT NULL, agreement_id uniqueidentifier NOT NULL,
    milestone_days int NOT NULL CHECK(milestone_days IN(90,60,30,14,7)),
    cycle_start_date date NOT NULL, due_at date NOT NULL,
    status nvarchar(10) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'open'
        CHECK(status IN(N'open',N'contacted',N'renewed',N'lost') AND DATALENGTH(status)=DATALENGTH(RTRIM(status))),
    notes nvarchar(max), updated_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    UNIQUE(shop_id,agreement_id,cycle_start_date,milestone_days),
    FOREIGN KEY(shop_id,agreement_id) REFERENCES dbo.agreements(shop_id,id) ON DELETE CASCADE
);
CREATE INDEX renewal_tasks_shop_due ON dbo.renewal_tasks(shop_id,due_at) WHERE status IN(N'open',N'contacted');
CREATE TABLE dbo.renewal_digest_outbox (
    id uniqueidentifier NOT NULL PRIMARY KEY DEFAULT NEWID(),
    shop_id uniqueidentifier NOT NULL REFERENCES dbo.shops(id) ON DELETE CASCADE,
    digest_date date NOT NULL,
    task_ids nvarchar(max) NOT NULL CHECK(ISJSON(task_ids,ARRAY)=1),
    status nvarchar(7) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'queued'
        CHECK(status IN(N'queued',N'sent',N'failed') AND DATALENGTH(status)=DATALENGTH(RTRIM(status))),
    created_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(), sent_at datetime2(6),
    UNIQUE(shop_id,digest_date)
);
CREATE INDEX renewal_digest_pending ON dbo.renewal_digest_outbox(status,created_at) WHERE status IN(N'queued',N'failed');
