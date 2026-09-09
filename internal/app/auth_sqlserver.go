package app

// Identity digests bound SQL Server indexes and lock names without imposing a
// new limit on existing text values. Full binary equality plus byte length is
// still required: neither a hash collision nor SQL's padded string comparison
// may turn a different stored identity into this caller's account.
const sqlServerLockAuthEmail = `
DECLARE @resource nvarchar(255)=N'clubdue-signup-email:'+CONVERT(varchar(64),
 HASHBYTES('SHA2_256',CONVERT(varbinary(max),CONVERT(nvarchar(max),@p1))),2);
DECLARE @result int;
EXEC @result=sys.sp_getapplock @Resource=@resource,@LockMode='Exclusive',@LockOwner='Transaction',@LockTimeout=10000;
IF @result<0 THROW 50001,'Could not lock account identity',1;`

const sqlServerOwnerByEmail = `
SELECT LOWER(CONVERT(char(36),id)),LOWER(CONVERT(char(36),shop_id))
FROM dbo.users WITH (UPDLOCK,HOLDLOCK)
WHERE email_key=HASHBYTES('SHA2_256',CONVERT(varbinary(max),CONVERT(nvarchar(max),@p1)))
 AND email=@p1 COLLATE Latin1_General_100_BIN2
 AND DATALENGTH(email)=DATALENGTH(CONVERT(nvarchar(max),@p1))`

const sqlServerNormalizedEmailExists = `
SELECT CAST(CASE WHEN EXISTS (
 SELECT 1 FROM dbo.users WITH (UPDLOCK,HOLDLOCK)
 WHERE email_key=HASHBYTES('SHA2_256',CONVERT(varbinary(max),CONVERT(nvarchar(max),@p1)))
 AND email_normalized=@p1 COLLATE Latin1_General_100_BIN2
 AND DATALENGTH(email_normalized)=DATALENGTH(CONVERT(nvarchar(max),@p1))
) THEN 1 ELSE 0 END AS bit)`

const sqlServerInsertUnclaimedShop = `
DECLARE @created TABLE (id uniqueidentifier);
INSERT INTO dbo.shops (name,slug) OUTPUT inserted.id INTO @created
SELECT @p1,@p2 WHERE NOT EXISTS (
 SELECT 1 FROM dbo.shops WITH (UPDLOCK,HOLDLOCK)
 WHERE slug_key=HASHBYTES('SHA2_256',CONVERT(varbinary(max),CONVERT(nvarchar(max),@p2)))
 AND slug=@p2 COLLATE Latin1_General_100_BIN2
 AND DATALENGTH(slug)=DATALENGTH(CONVERT(nvarchar(max),@p2))
);
SELECT LOWER(CONVERT(char(36),id)) FROM @created;`

const sqlServerInsertOwner = `
DECLARE @created TABLE (id uniqueidentifier);
INSERT INTO dbo.users (shop_id,email,email_normalized,password_hash,role) OUTPUT inserted.id INTO @created
VALUES (CONVERT(uniqueidentifier,@p1),@p2,@p2,@p3,'owner');
SELECT LOWER(CONVERT(char(36),id)) FROM @created;`

const sqlServerLoginByEmail = `
SELECT TOP (1) LOWER(CONVERT(char(36),u.id)),u.password_hash FROM dbo.users u
WHERE u.email_key=HASHBYTES('SHA2_256',CONVERT(varbinary(max),CONVERT(nvarchar(max),@p1)))
 AND u.email=@p1 COLLATE Latin1_General_100_BIN2
 AND DATALENGTH(u.email)=DATALENGTH(CONVERT(nvarchar(max),@p1))`

const sqlServerSessionUser = `
SELECT LOWER(CONVERT(char(36),u.id)),LOWER(CONVERT(char(36),u.shop_id)),u.email,u.role,s.name,
 CAST(CASE WHEN s.kind='demo_sandbox' THEN 1 ELSE 0 END AS bit)
FROM dbo.sessions sess JOIN dbo.users u ON u.id=sess.user_id JOIN dbo.shops s ON s.id=u.shop_id
WHERE sess.token_hash=@p1 AND sess.expires_at>@p2
 AND (s.expires_at IS NULL OR s.expires_at>@p2)`
