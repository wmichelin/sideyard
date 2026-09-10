CREATE TRIGGER dbo.asset_notes_validate_owner ON dbo.asset_notes AFTER INSERT,UPDATE AS
BEGIN
    SET NOCOUNT ON;
    -- Locking reads retain the parent through commit, including under RCSI.
    IF EXISTS(SELECT 1 FROM inserted i WHERE i.vehicle_id IS NOT NULL AND NOT EXISTS(
        SELECT 1 FROM dbo.customer_vehicles v WITH(UPDLOCK,HOLDLOCK) WHERE v.shop_id=i.shop_id AND v.id=i.vehicle_id))
        THROW 51001,'Asset note vehicle ownership is invalid.',1;
    IF EXISTS(SELECT 1 FROM inserted i WHERE i.equipment_id IS NOT NULL AND NOT EXISTS(
        SELECT 1 FROM dbo.equipment e WITH(UPDLOCK,HOLDLOCK) WHERE e.shop_id=i.shop_id AND e.id=i.equipment_id))
        THROW 51001,'Asset note equipment ownership is invalid.',1;
END;
