-- Preserve unbounded existing text identities. Digests keep indexes bounded;
-- repository lookups also compare the complete text and its byte length.
-- Closed values reserve one extra byte so SQL cannot silently truncate a
-- trailing space at the valid maximum before the exact-value CHECK executes.
CREATE TABLE dbo.shops (
    id uniqueidentifier NOT NULL PRIMARY KEY DEFAULT NEWID(),
    name nvarchar(max) NOT NULL,
    slug nvarchar(max) COLLATE Latin1_General_100_BIN2 NOT NULL,
    slug_key AS CONVERT(binary(32),HASHBYTES('SHA2_256',CONVERT(varbinary(max),slug))) PERSISTED,
    created_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    kind varchar(13) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT 'customer'
        CHECK (kind IN ('customer','demo_sandbox') AND LEN(kind)=DATALENGTH(kind)),
    expires_at datetime2(6),
    demo_mutations_used int NOT NULL DEFAULT 0 CHECK (demo_mutations_used>=0),
    demo_import_rows_used int NOT NULL DEFAULT 0 CHECK (demo_import_rows_used>=0),
    demo_import_occurrences_used int NOT NULL DEFAULT 0 CHECK (demo_import_occurrences_used>=0),
    CHECK ((kind='customer' AND expires_at IS NULL) OR (kind='demo_sandbox' AND expires_at IS NOT NULL))
);
CREATE UNIQUE INDEX shops_slug_key ON dbo.shops(slug_key);
CREATE INDEX shops_demo_expiry ON dbo.shops(expires_at) WHERE kind='demo_sandbox';

CREATE TABLE dbo.users (
    id uniqueidentifier NOT NULL PRIMARY KEY DEFAULT NEWID(),
    shop_id uniqueidentifier NOT NULL REFERENCES dbo.shops(id) ON DELETE CASCADE,
    email nvarchar(max) COLLATE Latin1_General_100_BIN2 NOT NULL,
    -- Go owns Unicode lowercasing; SQL Server LOWER differs from PostgreSQL.
    -- Migration must verify this derived value against the source identity.
    email_normalized nvarchar(max) COLLATE Latin1_General_100_BIN2 NOT NULL,
    email_key AS CONVERT(binary(32),HASHBYTES('SHA2_256',CONVERT(varbinary(max),email_normalized))) PERSISTED,
    password_hash nvarchar(max) NOT NULL,
    role varchar(6) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT 'owner'
        CHECK (role IN ('owner','staff') AND LEN(role)=DATALENGTH(role)),
    created_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME()
);
-- Global normalized identity also implies the older per-shop email uniqueness.
CREATE UNIQUE INDEX users_email_normalized_unique ON dbo.users(email_key);
CREATE INDEX users_shop ON dbo.users(shop_id);

CREATE TABLE dbo.sessions (
    id uniqueidentifier NOT NULL PRIMARY KEY DEFAULT NEWID(),
    user_id uniqueidentifier NOT NULL REFERENCES dbo.users(id) ON DELETE CASCADE,
    token_hash varchar(65) COLLATE Latin1_General_100_BIN2 NOT NULL UNIQUE
        CHECK (DATALENGTH(token_hash)=64 AND token_hash NOT LIKE '%[^0-9a-f]%'),
    expires_at datetime2(6) NOT NULL,
    created_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME()
);
CREATE INDEX sessions_expires_at ON dbo.sessions(expires_at);
CREATE INDEX sessions_user_id ON dbo.sessions(user_id);

CREATE TABLE dbo.owner_provision_events (
    id uniqueidentifier NOT NULL PRIMARY KEY DEFAULT NEWID(),
    user_id uniqueidentifier NOT NULL REFERENCES dbo.users(id) ON DELETE CASCADE,
    action varchar(12) COLLATE Latin1_General_100_BIN2 NOT NULL
        CHECK (action IN ('provisioned','recovered') AND LEN(action)=DATALENGTH(action)),
    created_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME()
);
CREATE INDEX owner_provision_events_user_created_at ON dbo.owner_provision_events(user_id,created_at DESC);

CREATE TABLE dbo.pilot_activation_events (
    id uniqueidentifier NOT NULL PRIMARY KEY DEFAULT NEWID(),
    shop_id uniqueidentifier NOT NULL REFERENCES dbo.shops(id) ON DELETE CASCADE,
    event_type varchar(32) COLLATE Latin1_General_100_BIN2 NOT NULL
        CHECK (event_type IN ('invited','account_created','import_previewed','import_committed','first_renewal_action','first_visit_action','returning_use') AND LEN(event_type)=DATALENGTH(event_type)),
    occurred_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    UNIQUE(shop_id,event_type)
);
CREATE INDEX pilot_activation_events_shop_occurred_at ON dbo.pilot_activation_events(shop_id,occurred_at);
