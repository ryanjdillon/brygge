-- Learned customer aliases: maps a Vipps display name to a club member.
-- Populated by the treasurer via the Vipps review queue (BRY-11 sub 6).
-- Checked before any fuzzy matching in resolveCustomerToMember so the
-- same payer is never re-asked.
CREATE TABLE vipps_customer_aliases (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id       UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    customer_name TEXT NOT NULL,
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX vipps_customer_aliases_name ON vipps_customer_aliases(club_id, lower(customer_name));
CREATE        INDEX vipps_customer_aliases_user ON vipps_customer_aliases(club_id, user_id);

-- Learned message rules: maps a pattern in the free-form Vipps message to
-- a GL account code. Higher priority wins when multiple rules match.
-- Pattern is matched case-insensitively as a substring (ILIKE '%pattern%').
CREATE TABLE vipps_message_rules (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id      UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    pattern      TEXT NOT NULL,
    account_code TEXT NOT NULL,
    priority     INT  NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX vipps_message_rules_pattern ON vipps_message_rules(club_id, lower(pattern));
CREATE        INDEX vipps_message_rules_priority ON vipps_message_rules(club_id, priority DESC);
