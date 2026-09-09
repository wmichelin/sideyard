ALTER TABLE properties ADD CONSTRAINT properties_customer_identity UNIQUE (shop_id,customer_id,id);

CREATE TABLE jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    shop_id UUID NOT NULL REFERENCES shops(id) ON DELETE CASCADE,
    customer_id UUID NOT NULL,
    property_id UUID,
    submission_key TEXT NOT NULL CHECK (length(submission_key)=32),
    creation_fingerprint TEXT NOT NULL CHECK (length(creation_fingerprint)=64),
    kind TEXT NOT NULL CHECK (kind IN ('job','survey')),
    title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 160),
    details TEXT NOT NULL DEFAULT '' CHECK (length(details)<=4000),
    scheduled_on DATE,
    status TEXT NOT NULL DEFAULT 'planned' CHECK (status IN ('planned','completed','cancelled')),
    completed_at TIMESTAMPTZ,
    revision INT NOT NULL DEFAULT 1 CHECK (revision>0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (shop_id,customer_id) REFERENCES customers(shop_id,id) ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (shop_id,customer_id,property_id) REFERENCES properties(shop_id,customer_id,id) ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED,
    CHECK ((status='completed')=(completed_at IS NOT NULL)),
    UNIQUE (shop_id,id),
    UNIQUE (shop_id,submission_key)
);
CREATE INDEX jobs_open_date ON jobs(shop_id,scheduled_on,id) WHERE status='planned';
CREATE INDEX jobs_customer ON jobs(shop_id,customer_id,created_at DESC,id);

CREATE TABLE job_events (
    shop_id UUID NOT NULL REFERENCES shops(id) ON DELETE CASCADE,
    job_id UUID NOT NULL,
    revision INT NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('created','edited','completed','cancelled','reopened')),
    snapshot JSONB NOT NULL,
    recorded_by TEXT NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (shop_id,job_id,revision),
    FOREIGN KEY (shop_id,job_id) REFERENCES jobs(shop_id,id) ON DELETE CASCADE
);
