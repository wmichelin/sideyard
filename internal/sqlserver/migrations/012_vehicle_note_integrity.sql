CREATE TRIGGER dbo.vehicle_note_integrity ON dbo.customer_vehicles AFTER UPDATE,DELETE AS
BEGIN
    SET NOCOUNT ON;
    IF NOT EXISTS(SELECT 1 FROM inserted)
    BEGIN
        DELETE n FROM dbo.asset_notes n JOIN deleted d ON d.shop_id=n.shop_id AND d.id=n.vehicle_id;
        RETURN;
    END;
    IF NOT(UPDATE(id) OR UPDATE(shop_id)) RETURN;
    IF EXISTS(SELECT 1 FROM dbo.asset_notes n WITH(UPDLOCK,HOLDLOCK)
        JOIN deleted d ON d.shop_id=n.shop_id AND d.id=n.vehicle_id
        WHERE NOT EXISTS(SELECT 1 FROM dbo.customer_vehicles v WITH(UPDLOCK,HOLDLOCK) WHERE v.shop_id=n.shop_id AND v.id=n.vehicle_id))
        THROW 51001,'Vehicle identity is referenced by service history.',1;
END;
