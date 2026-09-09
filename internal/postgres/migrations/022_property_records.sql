ALTER TABLE properties ADD CONSTRAINT properties_shop_identity UNIQUE (shop_id, id);

CREATE TABLE property_details (
    shop_id UUID NOT NULL REFERENCES shops(id) ON DELETE CASCADE,
    property_id UUID PRIMARY KEY,
    access_notes TEXT NOT NULL DEFAULT '' CHECK (length(access_notes) <= 2000),
    revision INT NOT NULL DEFAULT 1 CHECK (revision > 0),
    FOREIGN KEY (shop_id, property_id) REFERENCES properties(shop_id, id) ON DELETE CASCADE
);

-- Measurements are append-only observations. A new value does not rewrite old quotes.
CREATE TABLE property_measurements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    shop_id UUID NOT NULL REFERENCES shops(id) ON DELETE CASCADE,
    property_id UUID NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('turf','beds','driveway','walkway','patio','roof','building_footprint','floor_area','exterior_wall','perimeter','other_area','other_length')),
    zone TEXT NOT NULL CHECK (length(zone) BETWEEN 1 AND 120),
    value NUMERIC(14,3) NOT NULL CHECK (value > 0),
    unit TEXT NOT NULL CHECK (unit IN ('sq_ft','sq_m','ft','m')),
    source TEXT NOT NULL CHECK (length(source) BETWEEN 1 AND 120),
    measured_on DATE NOT NULL,
    estimated BOOLEAN NOT NULL,
    recorded_by TEXT NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (shop_id, property_id) REFERENCES properties(shop_id, id) ON DELETE CASCADE,
    CHECK ((kind IN ('perimeter','other_length') AND unit IN ('ft','m')) OR
           (kind NOT IN ('perimeter','other_length') AND unit IN ('sq_ft','sq_m')))
);
CREATE INDEX property_measurements_history ON property_measurements(shop_id, property_id, recorded_at DESC, id);
