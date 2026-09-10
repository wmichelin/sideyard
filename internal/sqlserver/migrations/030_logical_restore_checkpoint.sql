IF SCHEMA_ID(N'sideyard_transfer') IS NULL EXEC(N'CREATE SCHEMA sideyard_transfer');

CREATE TABLE sideyard_transfer.restore_state (
    singleton tinyint NOT NULL PRIMARY KEY CHECK(singleton=1),
    archive_sha varchar(64) NOT NULL,
    export_id uniqueidentifier NOT NULL,
    generation uniqueidentifier NOT NULL,
    root_sha varchar(64) NOT NULL,
    catalog_sha varchar(64) NOT NULL,
    next_table int NOT NULL CHECK(next_table>=0),
    table_rows bigint NOT NULL CHECK(table_rows>=0),
    completed bit NOT NULL,
    updated_at datetime2(6) NOT NULL
);
