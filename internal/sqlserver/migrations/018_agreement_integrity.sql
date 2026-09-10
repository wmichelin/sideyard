CREATE TRIGGER dbo.agreements_validate_references ON dbo.agreements AFTER INSERT,UPDATE AS
BEGIN
    SET NOCOUNT ON;
    IF EXISTS(SELECT 1 FROM inserted i CROSS APPLY OPENJSON(i.plan_snapshot_visit_windows) j WHERE j.type<>1)
        THROW 51001,'Agreement windows must contain only strings.',1;
    -- Only changed, non-null references need a parent lock. Independent property
    -- and plan deletion can clear their own pointer without locking each other.
    IF UPDATE(id) OR UPDATE(shop_id) OR UPDATE(customer_id) OR UPDATE(property_id)
    BEGIN
    IF EXISTS(SELECT 1 FROM inserted i LEFT JOIN deleted d ON d.id=i.id
        WHERE i.property_id IS NOT NULL
          AND (d.id IS NULL OR d.property_id IS NULL OR i.property_id<>d.property_id OR i.shop_id<>d.shop_id OR i.customer_id<>d.customer_id)
          AND NOT EXISTS(SELECT 1 FROM dbo.properties p WITH(HOLDLOCK)
              WHERE p.id=i.property_id AND p.shop_id=i.shop_id AND p.customer_id=i.customer_id))
        THROW 51001,'Agreement property ownership is invalid.',1;
    END;
    IF UPDATE(id) OR UPDATE(shop_id) OR UPDATE(plan_template_id)
    BEGIN
    IF EXISTS(SELECT 1 FROM inserted i LEFT JOIN deleted d ON d.id=i.id
        WHERE i.plan_template_id IS NOT NULL
          AND (d.id IS NULL OR d.plan_template_id IS NULL OR i.plan_template_id<>d.plan_template_id OR i.shop_id<>d.shop_id)
          AND NOT EXISTS(SELECT 1 FROM dbo.plan_templates p WITH(HOLDLOCK)
              WHERE p.id=i.plan_template_id AND p.shop_id=i.shop_id))
        THROW 51001,'Agreement plan ownership is invalid.',1;
    END;
END;
