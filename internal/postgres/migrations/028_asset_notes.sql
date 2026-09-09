ALTER TABLE customer_vehicles ADD CONSTRAINT customer_vehicles_shop_identity UNIQUE (shop_id,id);
ALTER TABLE equipment ADD CONSTRAINT equipment_shop_identity UNIQUE (shop_id,id);

CREATE TABLE asset_notes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    shop_id UUID NOT NULL REFERENCES shops(id) ON DELETE CASCADE,
    vehicle_id UUID,
    equipment_id UUID,
    submission_key TEXT NOT NULL CHECK (length(submission_key)=32),
    body TEXT NOT NULL CHECK (length(body) BETWEEN 1 AND 2000),
    occurred_on DATE NOT NULL,
    occurred_at TIMESTAMPTZ,
    event_timezone TEXT NOT NULL CHECK (length(event_timezone) BETWEEN 1 AND 100),
    recorded_by TEXT NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (num_nonnulls(vehicle_id,equipment_id)=1),
    FOREIGN KEY (shop_id,vehicle_id) REFERENCES customer_vehicles(shop_id,id) ON DELETE CASCADE,
    FOREIGN KEY (shop_id,equipment_id) REFERENCES equipment(shop_id,id) ON DELETE CASCADE,
    UNIQUE (shop_id,submission_key)
);
CREATE INDEX asset_notes_vehicle_history ON asset_notes(shop_id,vehicle_id,occurred_on DESC,recorded_at DESC,id DESC) WHERE vehicle_id IS NOT NULL;
CREATE INDEX asset_notes_equipment_history ON asset_notes(shop_id,equipment_id,occurred_on DESC,recorded_at DESC,id DESC) WHERE equipment_id IS NOT NULL;
