CREATE TABLE interest_signups (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    email TEXT NOT NULL UNIQUE CHECK (char_length(email) BETWEEN 3 AND 254 AND email = lower(email)),
    business TEXT NOT NULL DEFAULT '' CHECK (char_length(business) <= 160),
    trade TEXT NOT NULL DEFAULT '' CHECK (char_length(trade) <= 80),
    message TEXT NOT NULL DEFAULT '' CHECK (char_length(message) <= 2000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
