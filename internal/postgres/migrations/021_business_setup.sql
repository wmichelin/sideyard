-- Business presets are opt-in. Existing agreements and plan templates are untouched.
CREATE TABLE business_settings (
    shop_id UUID PRIMARY KEY REFERENCES shops(id) ON DELETE CASCADE,
    primary_trade TEXT NOT NULL,
    trades TEXT[] NOT NULL,
    revision INT NOT NULL DEFAULT 1 CHECK (revision > 0),
    CHECK (primary_trade = ANY(trades))
);

-- Draft service names are separate from annual maintenance plan templates.
CREATE TABLE business_services (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    shop_id UUID NOT NULL REFERENCES shops(id) ON DELETE CASCADE,
    preset_key TEXT NOT NULL,
    preset_version INT NOT NULL CHECK (preset_version > 0),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    description TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT true,
    revision INT NOT NULL DEFAULT 1 CHECK (revision > 0),
    UNIQUE (shop_id, preset_key)
);
