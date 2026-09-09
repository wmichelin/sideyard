-- SQL may discard trailing spaces at a column's physical width before CHECK.
-- Preserve the whole value for rune-limit checks, including a supplementary
-- character at the valid maximum followed by a space. Existing checks remain.
-- Version 001 generated CHECK names, so resolve only this table's constraints.
DECLARE @drop_checks nvarchar(max)=N'';
SELECT @drop_checks=@drop_checks+N'ALTER TABLE dbo.interest_signups DROP CONSTRAINT '+QUOTENAME(name)+N';'
FROM sys.check_constraints WHERE parent_object_id=OBJECT_ID(N'dbo.interest_signups');
EXEC sys.sp_executesql @drop_checks;
DECLARE @drop_defaults nvarchar(max)=N'';
SELECT @drop_defaults=@drop_defaults+N'ALTER TABLE dbo.interest_signups DROP CONSTRAINT '+QUOTENAME(d.name)+N';'
FROM sys.default_constraints d JOIN sys.columns c ON c.object_id=d.parent_object_id AND c.column_id=d.parent_column_id
WHERE d.parent_object_id=OBJECT_ID(N'dbo.interest_signups') AND c.name IN ('business','trade','message');
EXEC sys.sp_executesql @drop_defaults;
DROP INDEX interest_signups_email ON dbo.interest_signups;
ALTER TABLE dbo.interest_signups ALTER COLUMN name nvarchar(max) NOT NULL;
ALTER TABLE dbo.interest_signups ALTER COLUMN business nvarchar(max) NOT NULL;
ALTER TABLE dbo.interest_signups ALTER COLUMN trade nvarchar(max) NOT NULL;
ALTER TABLE dbo.interest_signups ALTER COLUMN message nvarchar(max) NOT NULL;
-- Keep email indexable, with room beyond its maximum valid UTF-16 width.
ALTER TABLE dbo.interest_signups ALTER COLUMN email nvarchar(509) COLLATE Latin1_General_100_BIN2 NOT NULL;
ALTER TABLE dbo.interest_signups ADD CONSTRAINT interest_business_default DEFAULT N'' FOR business;
ALTER TABLE dbo.interest_signups ADD CONSTRAINT interest_trade_default DEFAULT N'' FOR trade;
ALTER TABLE dbo.interest_signups ADD CONSTRAINT interest_message_default DEFAULT N'' FOR message;
ALTER TABLE dbo.interest_signups ADD
    CONSTRAINT interest_name_length CHECK (LEN(name COLLATE Latin1_General_100_CI_AS_SC+N'#')-1 BETWEEN 1 AND 120),
    CONSTRAINT interest_business_length CHECK (LEN(business COLLATE Latin1_General_100_CI_AS_SC+N'#')-1<=160),
    CONSTRAINT interest_trade_length CHECK (LEN(trade COLLATE Latin1_General_100_CI_AS_SC+N'#')-1<=80),
    CONSTRAINT interest_message_length CHECK (LEN(message COLLATE Latin1_General_100_CI_AS_SC+N'#')-1<=2000),
    CONSTRAINT interest_email_length CHECK (LEN(CONVERT(nvarchar(max),email) COLLATE Latin1_General_100_CI_AS_SC+N'#')-1 BETWEEN 3 AND 254 AND email=LOWER(email));
CREATE UNIQUE NONCLUSTERED INDEX interest_signups_email ON dbo.interest_signups(email);
