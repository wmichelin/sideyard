CREATE TRIGGER dbo.property_photos_retire_object ON dbo.property_photos AFTER UPDATE,DELETE AS
BEGIN
    SET NOCOUNT ON;
    UPDATE o SET retired_at=COALESCE(o.retired_at,SYSUTCDATETIME())
    FROM dbo.property_photo_objects o JOIN deleted d ON d.object_key=o.object_key
    LEFT JOIN inserted i ON i.id=d.id
    WHERE i.id IS NULL OR (d.deleted_at IS NULL AND i.deleted_at IS NOT NULL);
END;
