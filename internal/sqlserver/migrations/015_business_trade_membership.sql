CREATE TRIGGER dbo.business_settings_validate_trades ON dbo.business_settings AFTER INSERT,UPDATE AS
BEGIN
    SET NOCOUNT ON;
    -- OPENJSON's default value is MAX: historical trade IDs are not truncated.
    IF EXISTS(SELECT 1 FROM inserted i CROSS APPLY OPENJSON(i.trades) j WHERE j.type<>1)
        THROW 51001,'Business trades must contain only strings.',1;
    IF EXISTS(SELECT 1 FROM inserted i WHERE NOT EXISTS(
        SELECT 1 FROM OPENJSON(i.trades) j
        WHERE j.value COLLATE Latin1_General_100_BIN2=i.primary_trade
          AND DATALENGTH(j.value)=DATALENGTH(i.primary_trade)))
        THROW 51001,'Business primary trade must be selected.',1;
END;
