CREATE TRIGGER dbo.seasonal_note_integrity ON dbo.visit_dues AFTER UPDATE,DELETE AS
BEGIN
    SET NOCOUNT ON;
    IF NOT EXISTS(SELECT 1 FROM inserted)
    BEGIN
        DELETE n FROM dbo.visit_notes n JOIN deleted d ON d.shop_id=n.shop_id AND d.id=n.seasonal_visit_id;
        RETURN;
    END;
    IF NOT(UPDATE(id) OR UPDATE(shop_id)) RETURN;
    IF EXISTS(SELECT 1 FROM dbo.visit_notes n WITH(UPDLOCK,HOLDLOCK)
        JOIN deleted d ON d.shop_id=n.shop_id AND d.id=n.seasonal_visit_id
        WHERE NOT EXISTS(SELECT 1 FROM dbo.visit_dues v WITH(UPDLOCK,HOLDLOCK) WHERE v.shop_id=n.shop_id AND v.id=n.seasonal_visit_id))
        THROW 51001,'Seasonal visit identity is referenced by service history.',1;
END;
