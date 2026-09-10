CREATE TABLE dbo.customers (
    id uniqueidentifier NOT NULL PRIMARY KEY DEFAULT NEWID(),
    shop_id uniqueidentifier NOT NULL REFERENCES dbo.shops(id) ON DELETE CASCADE,
    external_id nvarchar(max) COLLATE Latin1_General_100_BIN2,
    external_id_key AS CONVERT(binary(32),HASHBYTES('SHA2_256',CONVERT(varbinary(max),external_id))) PERSISTED,
    name nvarchar(max) NOT NULL,
    email nvarchar(max), phone nvarchar(max),
    created_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    updated_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    UNIQUE(shop_id,id)
);
CREATE UNIQUE INDEX customers_external_identity ON dbo.customers(shop_id,external_id_key) WHERE external_id IS NOT NULL;

CREATE TABLE dbo.properties (
    id uniqueidentifier NOT NULL PRIMARY KEY DEFAULT NEWID(),
    shop_id uniqueidentifier NOT NULL,
    customer_id uniqueidentifier NOT NULL,
    external_id nvarchar(max) COLLATE Latin1_General_100_BIN2,
    external_id_key AS CONVERT(binary(32),HASHBYTES('SHA2_256',CONVERT(varbinary(max),external_id))) PERSISTED,
    label nvarchar(max),
    address_line1 nvarchar(max) NOT NULL, address_line2 nvarchar(max),
    city nvarchar(max) NOT NULL, state nvarchar(max) NOT NULL, postal_code nvarchar(max) NOT NULL,
    address_revision int NOT NULL DEFAULT 1 CHECK(address_revision>0),
    created_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    updated_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    UNIQUE(shop_id,id), UNIQUE(shop_id,customer_id,id),
    FOREIGN KEY(shop_id,customer_id) REFERENCES dbo.customers(shop_id,id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX properties_external_identity ON dbo.properties(shop_id,external_id_key) WHERE external_id IS NOT NULL;
CREATE INDEX properties_customer ON dbo.properties(shop_id,customer_id);

CREATE TABLE dbo.equipment (
    id uniqueidentifier NOT NULL PRIMARY KEY DEFAULT NEWID(),
    shop_id uniqueidentifier NOT NULL,
    property_id uniqueidentifier NOT NULL,
    external_id nvarchar(max) COLLATE Latin1_General_100_BIN2,
    external_id_key AS CONVERT(binary(32),HASHBYTES('SHA2_256',CONVERT(varbinary(max),external_id))) PERSISTED,
    kind nvarchar(max) NOT NULL,
    make nvarchar(max), model nvarchar(max), serial_number nvarchar(max),
    installed_on date, warranty_until date,
    location nvarchar(max) NOT NULL DEFAULT N'' CHECK(LEN(location COLLATE Latin1_General_100_CI_AS_SC+N'#')-1<=120),
    internal_notes nvarchar(max) NOT NULL DEFAULT N'' CHECK(LEN(internal_notes COLLATE Latin1_General_100_CI_AS_SC+N'#')-1<=2000),
    revision int NOT NULL DEFAULT 1 CHECK(revision>0),
    created_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    UNIQUE(shop_id,id),
    FOREIGN KEY(shop_id,property_id) REFERENCES dbo.properties(shop_id,id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX equipment_external_identity ON dbo.equipment(shop_id,external_id_key) WHERE external_id IS NOT NULL;
CREATE INDEX equipment_property ON dbo.equipment(shop_id,property_id);

CREATE TABLE dbo.customer_vehicles (
    id uniqueidentifier NOT NULL PRIMARY KEY DEFAULT NEWID(),
    shop_id uniqueidentifier NOT NULL,
    customer_id uniqueidentifier NOT NULL,
    name nvarchar(max) NOT NULL CHECK(LEN(name COLLATE Latin1_General_100_CI_AS_SC+N'#')-1 BETWEEN 1 AND 120),
    make nvarchar(max) NOT NULL DEFAULT N'', model nvarchar(max) NOT NULL DEFAULT N'',
    model_year int CHECK(model_year BETWEEN 1886 AND 9999),
    identifier nvarchar(max) NOT NULL DEFAULT N'',
    internal_notes nvarchar(max) NOT NULL DEFAULT N'' CHECK(LEN(internal_notes COLLATE Latin1_General_100_CI_AS_SC+N'#')-1<=2000),
    revision int NOT NULL DEFAULT 1 CHECK(revision>0),
    UNIQUE(shop_id,id),
    FOREIGN KEY(shop_id,customer_id) REFERENCES dbo.customers(shop_id,id) ON DELETE CASCADE
);
CREATE INDEX customer_vehicles_owner ON dbo.customer_vehicles(shop_id,customer_id);
