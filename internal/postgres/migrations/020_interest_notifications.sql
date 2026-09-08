CREATE TABLE interest_notifications (
    signup_id UUID PRIMARY KEY REFERENCES interest_signups(id) ON DELETE CASCADE,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at TIMESTAMPTZ,
    provider_message_id TEXT NOT NULL DEFAULT ''
);

CREATE INDEX interest_notifications_pending
    ON interest_notifications (next_attempt_at) WHERE sent_at IS NULL;
