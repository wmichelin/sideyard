-- Native interest-signup/outbox schema. Unicode limits count characters with a
-- supplementary-character-aware collation, including trailing spaces. Binary
-- collation keeps stored identity comparisons case-sensitive.
CREATE TABLE dbo.interest_signups (
    id uniqueidentifier NOT NULL PRIMARY KEY DEFAULT NEWID(),
    name nvarchar(240) NOT NULL
        CHECK (LEN(name COLLATE Latin1_General_100_CI_AS_SC + N'#')-1 BETWEEN 1 AND 120),
    email nvarchar(508) COLLATE Latin1_General_100_BIN2 NOT NULL
        CHECK (LEN(email COLLATE Latin1_General_100_CI_AS_SC + N'#')-1 BETWEEN 3 AND 254 AND email=LOWER(email)),
    business nvarchar(320) NOT NULL DEFAULT N''
        CHECK (LEN(business COLLATE Latin1_General_100_CI_AS_SC + N'#')-1 <= 160),
    trade nvarchar(160) NOT NULL DEFAULT N''
        CHECK (LEN(trade COLLATE Latin1_General_100_CI_AS_SC + N'#')-1 <= 80),
    message nvarchar(4000) NOT NULL DEFAULT N''
        CHECK (LEN(CONVERT(nvarchar(max),message) COLLATE Latin1_General_100_CI_AS_SC + N'#')-1 <= 2000),
    created_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME()
);
CREATE UNIQUE NONCLUSTERED INDEX interest_signups_email ON dbo.interest_signups(email);

CREATE TABLE dbo.interest_notifications (
    signup_id uniqueidentifier NOT NULL PRIMARY KEY REFERENCES dbo.interest_signups(id) ON DELETE CASCADE,
    attempts int NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    sent_at datetime2(6),
    provider_message_id nvarchar(max) NOT NULL DEFAULT N''
);
CREATE INDEX interest_notifications_pending ON dbo.interest_notifications(next_attempt_at) WHERE sent_at IS NULL;
