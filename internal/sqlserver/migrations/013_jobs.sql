CREATE TABLE dbo.jobs (
    id uniqueidentifier NOT NULL PRIMARY KEY DEFAULT NEWID(),
    shop_id uniqueidentifier NOT NULL REFERENCES dbo.shops(id) ON DELETE CASCADE,
    customer_id uniqueidentifier NOT NULL,
    property_id uniqueidentifier,
    submission_key nvarchar(65) COLLATE Latin1_General_100_BIN2 NOT NULL CHECK(LEN(submission_key COLLATE Latin1_General_100_CI_AS_SC+CONVERT(nvarchar(max),N'#'))-1=32),
    creation_fingerprint nvarchar(129) COLLATE Latin1_General_100_BIN2 NOT NULL CHECK(LEN(creation_fingerprint COLLATE Latin1_General_100_CI_AS_SC+CONVERT(nvarchar(max),N'#'))-1=64),
    kind nvarchar(7) COLLATE Latin1_General_100_BIN2 NOT NULL CHECK(kind IN(N'job',N'survey') AND DATALENGTH(kind)=DATALENGTH(RTRIM(kind))),
    title nvarchar(max) NOT NULL CHECK(LEN(title COLLATE Latin1_General_100_CI_AS_SC+N'#')-1 BETWEEN 1 AND 160),
    details nvarchar(max) NOT NULL DEFAULT N'' CHECK(LEN(details COLLATE Latin1_General_100_CI_AS_SC+N'#')-1<=4000),
    scheduled_on date,
    status nvarchar(10) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'planned' CHECK(status IN(N'planned',N'completed',N'cancelled') AND DATALENGTH(status)=DATALENGTH(RTRIM(status))),
    completed_at datetime2(6),
    revision int NOT NULL DEFAULT 1 CHECK(revision>0),
    created_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    updated_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    FOREIGN KEY(shop_id,customer_id) REFERENCES dbo.customers(shop_id,id),
    FOREIGN KEY(shop_id,customer_id,property_id) REFERENCES dbo.properties(shop_id,customer_id,id),
    CHECK((status=N'completed' AND completed_at IS NOT NULL) OR (status<>N'completed' AND completed_at IS NULL)),
    UNIQUE(shop_id,id), UNIQUE(shop_id,submission_key)
);
CREATE INDEX jobs_open_date ON dbo.jobs(shop_id,scheduled_on,id) WHERE status=N'planned';
CREATE INDEX jobs_customer ON dbo.jobs(shop_id,customer_id,created_at DESC,id);
CREATE INDEX jobs_property ON dbo.jobs(shop_id,customer_id,property_id);

CREATE TABLE dbo.job_events (
    shop_id uniqueidentifier NOT NULL,
    job_id uniqueidentifier NOT NULL,
    revision int NOT NULL,
    action nvarchar(10) COLLATE Latin1_General_100_BIN2 NOT NULL CHECK(action IN(N'created',N'edited',N'completed',N'cancelled',N'reopened') AND DATALENGTH(action)=DATALENGTH(RTRIM(action))),
    snapshot nvarchar(max) NOT NULL CHECK(ISJSON(snapshot,VALUE)=1),
    recorded_by nvarchar(max) NOT NULL,
    recorded_at datetime2(6) NOT NULL,
    PRIMARY KEY(shop_id,job_id,revision),
    FOREIGN KEY(shop_id,job_id) REFERENCES dbo.jobs(shop_id,id) ON DELETE CASCADE
);
