ALTER TABLE equipment
    ADD COLUMN location TEXT NOT NULL DEFAULT '' CHECK (length(location) <= 120),
    ADD COLUMN warranty_until DATE,
    ADD COLUMN internal_notes TEXT NOT NULL DEFAULT '' CHECK (length(internal_notes) <= 2000),
    ADD COLUMN revision INT NOT NULL DEFAULT 1 CHECK (revision > 0),
    ADD CONSTRAINT equipment_property_ownership
        FOREIGN KEY (shop_id, property_id) REFERENCES properties(shop_id, id) ON DELETE CASCADE;
