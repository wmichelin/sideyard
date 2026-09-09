ALTER TABLE customers ADD CONSTRAINT customers_shop_identity UNIQUE (shop_id, id);

CREATE TABLE customer_vehicles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    shop_id UUID NOT NULL REFERENCES shops(id) ON DELETE CASCADE,
    customer_id UUID NOT NULL,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    make TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    model_year INT CHECK (model_year BETWEEN 1886 AND 9999),
    identifier TEXT NOT NULL DEFAULT '',
    internal_notes TEXT NOT NULL DEFAULT '' CHECK (length(internal_notes) <= 2000),
    revision INT NOT NULL DEFAULT 1 CHECK (revision > 0),
    FOREIGN KEY (shop_id, customer_id) REFERENCES customers(shop_id, id) ON DELETE CASCADE
);
CREATE INDEX customer_vehicles_owner ON customer_vehicles(shop_id, customer_id);
