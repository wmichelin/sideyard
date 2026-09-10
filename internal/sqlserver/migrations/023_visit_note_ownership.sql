CREATE TRIGGER dbo.visit_notes_validate_owner ON dbo.visit_notes AFTER INSERT,UPDATE AS
BEGIN
    SET NOCOUNT ON;
    -- Locking reads retain the parent through commit, including under RCSI.
    IF EXISTS(SELECT 1 FROM inserted i WHERE i.occurrence_id IS NOT NULL AND NOT EXISTS(
        SELECT 1 FROM dbo.service_occurrences v WITH(UPDLOCK,HOLDLOCK) WHERE v.shop_id=i.shop_id AND v.id=i.occurrence_id))
        THROW 51001,'Visit note recurring ownership is invalid.',1;
    IF EXISTS(SELECT 1 FROM inserted i WHERE i.seasonal_visit_id IS NOT NULL AND NOT EXISTS(
        SELECT 1 FROM dbo.visit_dues e WITH(UPDLOCK,HOLDLOCK) WHERE e.shop_id=i.shop_id AND e.id=i.seasonal_visit_id))
        THROW 51001,'Visit note seasonal ownership is invalid.',1;
END;
