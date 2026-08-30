ALTER TABLE agreements DROP CONSTRAINT agreements_status_check;
ALTER TABLE agreements ADD CONSTRAINT agreements_status_check
    CHECK (status IN ('active', 'past_due', 'lapsed', 'cancelled', 'pending_renewal'));
