-- Phase 1.2: explicit agreement term for reusable plan templates.

ALTER TABLE plan_templates
    ADD COLUMN term_months INT NOT NULL DEFAULT 12 CHECK (term_months > 0);
