-- Role-based price exemptions (BRY-351). A club can mark a price item, or a
-- whole price category, as exempt for a board role: when a faktura is
-- generated for a member holding that role, the item is billed at full price
-- and an offsetting "Fritak – <role>" line brings it back to zero. There are
-- no default rows; every club configures its own.

CREATE TABLE IF NOT EXISTS price_exemptions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id       UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    role          TEXT NOT NULL,
    category      TEXT,
    price_item_id UUID REFERENCES price_items(id) ON DELETE CASCADE,
    created_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'price_exemptions_one_target') THEN
        ALTER TABLE price_exemptions ADD CONSTRAINT price_exemptions_one_target
            CHECK ((category IS NULL) <> (price_item_id IS NULL));
    END IF;
END
$$;

CREATE UNIQUE INDEX IF NOT EXISTS idx_price_exemptions_unique
    ON price_exemptions (club_id, role, COALESCE(category, ''), COALESCE(price_item_id::text, ''));

-- Expense account (kontoplan code) the exemption lines are booked to, so the
-- waived revenue shows gross: CR the item's revenue, DR this account. NULL
-- means exemptions are not applied until the club picks one.
ALTER TABLE clubs ADD COLUMN IF NOT EXISTS exemption_expense_account TEXT;

-- Marks an invoice line as an exemption and records which role earned it,
-- for the faktura display and the exempted-amounts overview (BRY-353).
ALTER TABLE invoice_lines ADD COLUMN IF NOT EXISTS exemption_role TEXT;

-- The read-only query role reads the configuration raw: roles and price
-- categories name no one.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'brygge_dev_ro') THEN
        EXECUTE 'GRANT SELECT ON price_exemptions TO brygge_dev_ro';
    END IF;
END
$$;
