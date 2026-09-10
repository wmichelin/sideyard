CREATE TRIGGER dbo.renewal_digest_membership ON dbo.renewal_digest_outbox AFTER INSERT,UPDATE AS
BEGIN
    SET NOCOUNT ON;
    -- Preserve a nonempty, ordered UUID array including duplicates/NULL members.
    -- IDs deliberately need not reference an existing task: this is a snapshot.
    IF EXISTS(SELECT 1 FROM inserted i WHERE NOT EXISTS(SELECT 1 FROM OPENJSON(i.task_ids)))
        THROW 51001,'Renewal digest membership must not be empty.',1;
    IF EXISTS(SELECT 1 FROM inserted i CROSS APPLY OPENJSON(i.task_ids) j
        WHERE j.type<>0 AND (j.type<>1 OR DATALENGTH(j.value)<>72 OR TRY_CONVERT(uniqueidentifier,j.value) IS NULL
        OR LOWER(j.value) COLLATE Latin1_General_100_BIN2 <> LOWER(CONVERT(char(36),TRY_CONVERT(uniqueidentifier,j.value))) COLLATE Latin1_General_100_BIN2))
        THROW 51001,'Renewal digest membership must contain UUIDs or null.',1;
END;
