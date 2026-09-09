ALTER TABLE properties ADD COLUMN address_revision INT NOT NULL DEFAULT 1 CHECK (address_revision > 0);

-- Address changes through existing imports must invalidate open edit forms too.
CREATE FUNCTION advance_property_address_revision() RETURNS TRIGGER AS $$
BEGIN
    IF ROW(NEW.address_line1, NEW.address_line2, NEW.city, NEW.state, NEW.postal_code)
       IS DISTINCT FROM ROW(OLD.address_line1, OLD.address_line2, OLD.city, OLD.state, OLD.postal_code) THEN
        NEW.address_revision := OLD.address_revision + 1;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER properties_address_revision
    BEFORE UPDATE OF address_line1, address_line2, city, state, postal_code ON properties
    FOR EACH ROW EXECUTE FUNCTION advance_property_address_revision();
