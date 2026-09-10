-- SQL Server rejects the two cascading routes through recurring and seasonal visits.
-- Visit references are enforced by the following validation/cleanup triggers;
-- restore must fire them and audit ownership, not only CHECKCONSTRAINTS.
CREATE TABLE dbo.visit_notes (
    id uniqueidentifier NOT NULL PRIMARY KEY DEFAULT NEWID(),
    shop_id uniqueidentifier NOT NULL REFERENCES dbo.shops(id) ON DELETE CASCADE,
    occurrence_id uniqueidentifier,
    seasonal_visit_id uniqueidentifier,
    submission_key nvarchar(65) COLLATE Latin1_General_100_BIN2 NOT NULL CHECK(LEN(submission_key COLLATE Latin1_General_100_CI_AS_SC+CONVERT(nvarchar(max),N'#'))-1=32),
    body nvarchar(max) NOT NULL CHECK(LEN(body COLLATE Latin1_General_100_CI_AS_SC+N'#')-1 BETWEEN 1 AND 2000),
    occurred_on date NOT NULL,
    occurred_at datetime2(6),
    event_timezone nvarchar(max) NOT NULL CHECK(LEN(event_timezone COLLATE Latin1_General_100_CI_AS_SC+N'#')-1 BETWEEN 1 AND 100),
    recorded_by nvarchar(max) NOT NULL,
    recorded_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    CHECK((occurrence_id IS NOT NULL AND seasonal_visit_id IS NULL) OR (occurrence_id IS NULL AND seasonal_visit_id IS NOT NULL)),
    UNIQUE(shop_id,submission_key)
);
CREATE INDEX visit_notes_occurrence_history ON dbo.visit_notes(shop_id,occurrence_id,occurred_on DESC,recorded_at DESC,id DESC) WHERE occurrence_id IS NOT NULL;
CREATE INDEX visit_notes_seasonal_history ON dbo.visit_notes(shop_id,seasonal_visit_id,occurred_on DESC,recorded_at DESC,id DESC) WHERE seasonal_visit_id IS NOT NULL;
