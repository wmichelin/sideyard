CREATE TRIGGER dbo.plan_templates_integrity ON dbo.plan_templates AFTER INSERT,UPDATE,DELETE AS
BEGIN
    SET NOCOUNT ON;
    IF EXISTS(SELECT 1 FROM inserted i CROSS APPLY OPENJSON(i.visit_windows) j WHERE j.type<>1)
        THROW 51001,'Plan windows must contain only strings.',1;
    IF NOT EXISTS(SELECT 1 FROM inserted)
    BEGIN
        -- Pointer cleanup leaves captured terms and agreement timestamps intact.
        UPDATE a SET plan_template_id=NULL FROM dbo.agreements a JOIN deleted d ON d.id=a.plan_template_id;
        RETURN;
    END;
    IF UPDATE(id) OR UPDATE(shop_id)
    BEGIN
        IF EXISTS(SELECT 1 FROM deleted d JOIN dbo.agreements a WITH(UPDLOCK,HOLDLOCK) ON a.plan_template_id=d.id
            WHERE NOT EXISTS(SELECT 1 FROM dbo.plan_templates p WITH(HOLDLOCK) WHERE p.id=a.plan_template_id AND p.shop_id=a.shop_id))
            THROW 51001,'Plan change would invalidate an agreement.',1;
    END;
END;
