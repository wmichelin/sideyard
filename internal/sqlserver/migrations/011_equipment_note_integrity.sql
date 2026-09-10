CREATE TRIGGER dbo.equipment_note_integrity ON dbo.equipment AFTER UPDATE,DELETE AS
BEGIN
    SET NOCOUNT ON;
    IF NOT EXISTS(SELECT 1 FROM inserted)
    BEGIN
        DELETE n FROM dbo.asset_notes n JOIN deleted d ON d.shop_id=n.shop_id AND d.id=n.equipment_id;
        RETURN;
    END;
    IF NOT(UPDATE(id) OR UPDATE(shop_id)) RETURN;
    IF EXISTS(SELECT 1 FROM dbo.asset_notes n WITH(UPDLOCK,HOLDLOCK)
        JOIN deleted d ON d.shop_id=n.shop_id AND d.id=n.equipment_id
        WHERE NOT EXISTS(SELECT 1 FROM dbo.equipment e WITH(UPDLOCK,HOLDLOCK) WHERE e.shop_id=n.shop_id AND e.id=n.equipment_id))
        THROW 51001,'Equipment identity is referenced by service history.',1;
END;
