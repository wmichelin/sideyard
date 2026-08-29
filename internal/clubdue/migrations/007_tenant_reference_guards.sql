-- Tenant keys are UUIDs, so ordinary foreign keys prove existence but not
-- ownership. Keep the invariant in the database so imports and future write
-- paths cannot accidentally bypass the application layer.
CREATE OR REPLACE FUNCTION validate_agreement_references()
RETURNS TRIGGER AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM customers
        WHERE id = NEW.customer_id AND shop_id = NEW.shop_id
    ) THEN
        RAISE EXCEPTION 'agreement customer must belong to the shop'
            USING ERRCODE = 'foreign_key_violation';
    END IF;

    IF NEW.property_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM properties
        WHERE id = NEW.property_id
          AND shop_id = NEW.shop_id
          AND customer_id = NEW.customer_id
    ) THEN
        RAISE EXCEPTION 'agreement property must belong to the selected customer and shop'
            USING ERRCODE = 'foreign_key_violation';
    END IF;

    IF NEW.plan_template_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM plan_templates
        WHERE id = NEW.plan_template_id AND shop_id = NEW.shop_id
    ) THEN
        RAISE EXCEPTION 'agreement plan template must belong to the shop'
            USING ERRCODE = 'foreign_key_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER agreements_validate_references
    BEFORE INSERT OR UPDATE OF shop_id, customer_id, property_id, plan_template_id
    ON agreements
    FOR EACH ROW EXECUTE FUNCTION validate_agreement_references();

-- Login is email-only, so its identity must be unambiguous across shops.
CREATE UNIQUE INDEX users_email_normalized_unique ON users (lower(email));
