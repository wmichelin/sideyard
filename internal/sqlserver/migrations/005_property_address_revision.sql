CREATE TRIGGER dbo.properties_address_revision ON dbo.properties AFTER UPDATE AS
BEGIN
    SET NOCOUNT ON;
    IF TRIGGER_NESTLEVEL(OBJECT_ID(N'dbo.properties_address_revision'),'AFTER','DML')>1 RETURN;
    IF NOT (UPDATE(address_line1) OR UPDATE(address_line2) OR UPDATE(city) OR UPDATE(state) OR UPDATE(postal_code)) RETURN;
    -- EXCEPT treats NULLs as equal; binary values preserve case, Unicode and
    -- trailing spaces. SQL's normal padded string equality would miss changes.
    UPDATE p SET address_revision=d.address_revision+1
    FROM dbo.properties p JOIN inserted i ON i.id=p.id JOIN deleted d ON d.id=i.id
    WHERE EXISTS (
        SELECT CONVERT(varbinary(max),i.address_line1),CONVERT(varbinary(max),i.address_line2),
            CONVERT(varbinary(max),i.city),CONVERT(varbinary(max),i.state),CONVERT(varbinary(max),i.postal_code),
            DATALENGTH(i.address_line1),DATALENGTH(i.address_line2),DATALENGTH(i.city),DATALENGTH(i.state),DATALENGTH(i.postal_code)
        EXCEPT
        SELECT CONVERT(varbinary(max),d.address_line1),CONVERT(varbinary(max),d.address_line2),
            CONVERT(varbinary(max),d.city),CONVERT(varbinary(max),d.state),CONVERT(varbinary(max),d.postal_code),
            DATALENGTH(d.address_line1),DATALENGTH(d.address_line2),DATALENGTH(d.city),DATALENGTH(d.state),DATALENGTH(d.postal_code)
    );
END;
