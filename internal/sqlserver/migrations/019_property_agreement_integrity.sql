CREATE TRIGGER dbo.properties_agreement_integrity ON dbo.properties AFTER UPDATE,DELETE AS
BEGIN
    SET NOCOUNT ON;
    IF NOT EXISTS(SELECT 1 FROM inserted)
    BEGIN
        UPDATE a SET property_id=NULL FROM dbo.agreements a JOIN deleted d ON d.id=a.property_id;
        RETURN;
    END;
    IF UPDATE(id) OR UPDATE(shop_id) OR UPDATE(customer_id)
    BEGIN
        IF EXISTS(SELECT 1 FROM deleted d JOIN dbo.agreements a WITH(UPDLOCK,HOLDLOCK) ON a.property_id=d.id
            WHERE NOT EXISTS(SELECT 1 FROM dbo.properties p WITH(HOLDLOCK)
                WHERE p.id=a.property_id AND p.shop_id=a.shop_id AND p.customer_id=a.customer_id))
            THROW 51001,'Property change would invalidate an agreement.',1;
    END;
END;
