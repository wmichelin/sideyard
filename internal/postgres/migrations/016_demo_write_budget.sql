-- Public demo writes are interactive but remain durably bounded for the
-- lifetime of each isolated sandbox. Customer shops retain the default and
-- never consume this counter.

ALTER TABLE shops
    ADD COLUMN demo_mutations_used INTEGER NOT NULL DEFAULT 0
        CHECK (demo_mutations_used >= 0);
