CREATE TRIGGER dbo.recurring_note_integrity ON dbo.service_occurrences AFTER UPDATE,DELETE AS
BEGIN
    SET NOCOUNT ON;
    IF NOT EXISTS(SELECT 1 FROM inserted)
    BEGIN
        DELETE n FROM dbo.visit_notes n JOIN deleted d ON d.shop_id=n.shop_id AND d.id=n.occurrence_id;
        RETURN;
    END;
    IF NOT(UPDATE(id) OR UPDATE(shop_id)) RETURN;
    IF EXISTS(SELECT 1 FROM dbo.visit_notes n WITH(UPDLOCK,HOLDLOCK)
        JOIN deleted d ON d.shop_id=n.shop_id AND d.id=n.occurrence_id
        WHERE NOT EXISTS(SELECT 1 FROM dbo.service_occurrences e WITH(UPDLOCK,HOLDLOCK) WHERE e.shop_id=n.shop_id AND e.id=n.occurrence_id))
        THROW 51001,'Recurring visit identity is referenced by service history.',1;
END;
